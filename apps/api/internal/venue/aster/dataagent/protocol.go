package dataagent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	asteraccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/account"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	secpECDSA "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

const (
	defaultBaseURL  = "https://fapi.asterdex.com"
	requestTimeout  = 10 * time.Second
	maxReadResponse = 1 << 20
	incomeWindow    = 7 * 24 * time.Hour
)

var readPaths = map[string]bool{
	"/fapi/v3/agent": true, "/fapi/v3/accountWithJoinMargin": true,
	"/fapi/v3/positionRisk": true, "/fapi/v3/income": true,
}

type EndpointReport struct {
	Agent        bool `json:"agent"`
	Account      bool `json:"account"`
	PositionRisk bool `json:"position_risk"`
	Income       bool `json:"income"`
}

type AgentPermissions struct {
	CanRead      bool `json:"canRead"`
	CanSpotTrade bool `json:"canSpotTrade"`
	CanPerpTrade bool `json:"canPerpTrade"`
	CanWithdraw  bool `json:"canWithdraw"`
}

type Report struct {
	Endpoints               EndpointReport    `json:"endpoints"`
	MatchedAgentPermissions *AgentPermissions `json:"matched_agent_permissions,omitempty"`
	RequestedExpiry         int64             `json:"requested_expiry"`
	ReportedExpiry          int64             `json:"reported_expiry,omitempty"`
	ExecutionAgentPreserved bool              `json:"execution_agent_preserved"`
	Success                 bool              `json:"success"`
	Error                   string            `json:"error,omitempty"`
}

type Approval struct {
	User             string `json:"user"`
	Nonce            int64  `json:"nonce"`
	AgentName        string `json:"agentName"`
	AgentAddress     string `json:"agentAddress"`
	IPWhitelist      string `json:"ipWhitelist"`
	Expired          int64  `json:"expired"`
	CanSpotTrade     bool   `json:"canSpotTrade"`
	CanPerpTrade     bool   `json:"canPerpTrade"`
	CanWithdraw      bool   `json:"canWithdraw"`
	AsterChain       string `json:"asterChain"`
	SignatureChainID int    `json:"signatureChainId"`
}

type Prepared struct {
	ProbeID  string   `json:"probe_id"`
	Approval Approval `json:"approval"`
}

type Client struct {
	baseURL string
	http    *http.Client
	now     func() time.Time
	nonce   atomic.Int64
}

func NewClient(baseURL string, httpClient *http.Client, now func() time.Time) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if now == nil {
		now = time.Now
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	client := *httpClient
	client.Timeout = requestTimeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &client, now: now}
}

func NewDefaultClient() *Client {
	return NewClient(defaultBaseURL, &http.Client{}, time.Now)
}

func (c *Client) Approve(ctx context.Context, approval Approval, signature string) error {
	if ctx.Err() != nil {
		return ErrApprovalNotSent
	}
	params := []pair{
		{"agentName", approval.AgentName}, {"agentAddress", approval.AgentAddress},
		{"ipWhitelist", approval.IPWhitelist}, {"expired", strconv.FormatInt(approval.Expired, 10)},
		{"canSpotTrade", strconv.FormatBool(approval.CanSpotTrade)}, {"canPerpTrade", strconv.FormatBool(approval.CanPerpTrade)},
		{"canWithdraw", strconv.FormatBool(approval.CanWithdraw)}, {"asterChain", approval.AsterChain},
		{"user", approval.User}, {"nonce", strconv.FormatInt(approval.Nonce, 10)},
		{"signatureChainId", strconv.Itoa(approval.SignatureChainID)}, {"signature", signature},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/fapi/v3/approveAgent", strings.NewReader(encode(params)))
	if err != nil {
		return ErrApprovalNotSent
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, status, err := c.do(request, 64<<10)
	if err != nil {
		return ErrApprovalAmbiguous
	}
	if status >= 500 || status == http.StatusTooManyRequests || status == http.StatusRequestTimeout {
		return ErrApprovalAmbiguous
	}
	if status < 200 || status >= 300 {
		return ErrApprovalRejected
	}
	var result struct {
		Code int `json:"code"`
	}
	if json.Unmarshal(body, &result) != nil {
		return ErrApprovalAmbiguous
	}
	if result.Code != http.StatusOK {
		return ErrApprovalRejected
	}
	return nil
}

func (c *Client) Probe(ctx context.Context, owner, agent, executionAgent string, privateKey []byte, requestedExpiry int64) Report {
	report := Report{RequestedExpiry: requestedExpiry}
	type call struct {
		path    string
		params  []pair
		failure string
		valid   func([]byte) error
		set     func()
	}
	now := c.now()
	calls := []call{
		{path: "/fapi/v3/agent", failure: "Aster agent-list response was invalid", valid: func(body []byte) error {
			permissions, expiry, executionPreserved, err := validateAgentResponse(body, agent, executionAgent, now.UnixMilli())
			if err == nil {
				report.MatchedAgentPermissions = permissions
				report.ReportedExpiry = expiry
				report.ExecutionAgentPreserved = executionPreserved
			}
			return err
		}, set: func() { report.Endpoints.Agent = true }},
		{path: "/fapi/v3/accountWithJoinMargin", failure: "Aster account response was invalid", valid: validateAccount, set: func() { report.Endpoints.Account = true }},
		{path: "/fapi/v3/positionRisk", failure: "Aster position-risk response was invalid", valid: validatePositions, set: func() { report.Endpoints.PositionRisk = true }},
		{path: "/fapi/v3/income", params: []pair{
			{"incomeType", "FUNDING_FEE"}, {"startTime", strconv.FormatInt(now.Add(-incomeWindow).UnixMilli(), 10)},
			{"endTime", strconv.FormatInt(now.UnixMilli(), 10)}, {"limit", "100"},
		}, failure: "Aster funding-income response was invalid", valid: validateIncome, set: func() { report.Endpoints.Income = true }},
	}
	for _, item := range calls {
		body, err := c.signedGET(ctx, item.path, item.params, owner, agent, privateKey, c.nextNonce())
		if err != nil {
			report.Error = "Aster " + endpointName(item.path) + " read failed"
			return report
		}
		if err := item.valid(body); err != nil {
			report.Error = item.failure
			return report
		}
		item.set()
	}
	permissions := report.MatchedAgentPermissions
	if permissions == nil || !permissions.CanRead {
		report.Error = "Aster data agent is missing read permission"
		return report
	}
	if permissions.CanSpotTrade || permissions.CanPerpTrade || permissions.CanWithdraw {
		report.Error = "Aster data agent has unexpected write permission"
		return report
	}
	if report.ReportedExpiry <= now.Add(7*24*time.Hour).UnixMilli() || abs(report.ReportedExpiry-requestedExpiry) > int64(time.Minute/time.Millisecond) {
		report.Error = "Aster data-agent expiry does not match the request"
		return report
	}
	if !report.ExecutionAgentPreserved {
		report.Error = "expected Aster execution agent is missing"
		return report
	}
	report.Success = true
	return report
}

func (c *Client) signedGET(ctx context.Context, path string, params []pair, owner, agent string, privateKey []byte, nonce int64) ([]byte, error) {
	if !readPaths[path] || len(privateKey) != 32 {
		return nil, fmt.Errorf("Aster signed-read endpoint is not allowed")
	}
	params = append(append([]pair(nil), params...), pair{"asterChain", "Mainnet"}, pair{"user", strings.ToLower(owner)},
		pair{"signer", strings.ToLower(agent)}, pair{"nonce", strconv.FormatInt(nonce, 10)})
	query := encode(params)
	signature := signMessage(privateKey, query)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+query+"&signature="+url.QueryEscape(signature), nil)
	if err != nil {
		return nil, fmt.Errorf("build Aster signed read")
	}
	body, status, err := c.do(request, maxReadResponse)
	if err != nil || status < 200 || status >= 300 {
		return nil, fmt.Errorf("Aster signed read failed")
	}
	if venueResponseRejected(body) {
		return nil, fmt.Errorf("Aster signed read rejected")
	}
	return body, nil
}

func (c *Client) do(request *http.Request, limit int64) ([]byte, int, error) {
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, response.StatusCode, errors.New("invalid Aster response size")
	}
	return body, response.StatusCode, nil
}

func (c *Client) nextNonce() int64 {
	for {
		previous := c.nonce.Load()
		next := c.now().UnixMicro()
		if next <= previous {
			next = previous + 1
		}
		if c.nonce.CompareAndSwap(previous, next) {
			return next
		}
	}
}

type pair struct{ key, value string }

func encode(params []pair) string {
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(parts, url.QueryEscape(param.key)+"="+url.QueryEscape(param.value))
	}
	return strings.Join(parts, "&")
}

func signMessage(privateKey []byte, query string) string {
	domain := keccak(
		keccak([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")),
		keccak([]byte("AsterSignTransaction")), keccak([]byte("1")), uintWord(1666), make([]byte, 32),
	)
	message := keccak(keccak([]byte("Message(string msg)")), keccak([]byte(query)))
	digest := keccak([]byte{0x19, 0x01}, domain, message)
	key := secp256k1.PrivKeyFromBytes(privateKey)
	defer key.Zero()
	compact := secpECDSA.SignCompact(key, digest, true)
	signature := append(append([]byte(nil), compact[1:]...), compact[0]-4)
	return "0x" + hex.EncodeToString(signature)
}

func addressFromPrivateKey(privateKey []byte) string {
	key := secp256k1.PrivKeyFromBytes(privateKey)
	defer key.Zero()
	publicKey := key.PubKey().SerializeUncompressed()
	hash := keccak(publicKey[1:])
	return "0x" + hex.EncodeToString(hash[len(hash)-20:])
}

func keccak(parts ...[]byte) []byte {
	hash := sha3.NewLegacyKeccak256()
	for _, part := range parts {
		_, _ = hash.Write(part)
	}
	return hash.Sum(nil)
}

func uintWord(value uint64) []byte {
	word := make([]byte, 32)
	for index := 0; index < 8; index++ {
		word[31-index] = byte(value >> (index * 8))
	}
	return word
}

func validateAgentResponse(body []byte, expectedDataAgent, expectedExecutionAgent string, now int64) (*AgentPermissions, int64, bool, error) {
	type agent struct {
		AgentAddress string `json:"agentAddress"`
		CanRead      *bool  `json:"canRead"`
		CanSpotTrade *bool  `json:"canSpotTrade"`
		CanPerpTrade *bool  `json:"canPerpTrade"`
		CanWithdraw  *bool  `json:"canWithdraw"`
		Expired      int64  `json:"expired"`
	}
	var agents []agent
	if err := json.Unmarshal(body, &agents); err != nil {
		var wrapped struct {
			Agents []agent `json:"agents"`
			Data   []agent `json:"data"`
		}
		if json.Unmarshal(body, &wrapped) != nil {
			return nil, 0, false, fmt.Errorf("invalid agent response")
		}
		agents = wrapped.Agents
		if agents == nil {
			agents = wrapped.Data
		}
	}
	executionPreserved := false
	var permissions *AgentPermissions
	var expiry int64
	for _, candidate := range agents {
		if strings.EqualFold(candidate.AgentAddress, expectedExecutionAgent) && candidate.CanRead != nil && *candidate.CanRead &&
			candidate.CanSpotTrade != nil && !*candidate.CanSpotTrade && candidate.CanPerpTrade != nil && *candidate.CanPerpTrade &&
			candidate.CanWithdraw != nil && !*candidate.CanWithdraw && candidate.Expired > now {
			executionPreserved = true
		}
		if strings.EqualFold(candidate.AgentAddress, expectedDataAgent) && candidate.CanRead != nil &&
			candidate.CanSpotTrade != nil && candidate.CanPerpTrade != nil && candidate.CanWithdraw != nil && candidate.Expired > 0 {
			permissions = &AgentPermissions{*candidate.CanRead, *candidate.CanSpotTrade, *candidate.CanPerpTrade, *candidate.CanWithdraw}
			expiry = candidate.Expired
		}
	}
	if permissions == nil {
		return nil, 0, executionPreserved, fmt.Errorf("data agent not found")
	}
	return permissions, expiry, executionPreserved, nil
}

func validateAccount(body []byte) error {
	_, err := asteraccount.ParseSnapshotPart("get_account", body)
	return err
}

func validatePositions(body []byte) error {
	_, err := asteraccount.ParsePositions(body)
	return err
}

func venueResponseRejected(body []byte) bool {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil {
		return false
	}
	raw, exists := envelope["code"]
	if !exists {
		return false
	}
	var number int
	if json.Unmarshal(raw, &number) == nil {
		return number != 0 && number != http.StatusOK
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return true
	}
	number, err := strconv.Atoi(text)
	return err != nil || (number != 0 && number != http.StatusOK)
}

func validateIncome(body []byte) error {
	var rows []struct {
		IncomeType string `json:"incomeType"`
	}
	if json.Unmarshal(body, &rows) != nil || rows == nil || len(rows) > 100 {
		return fmt.Errorf("invalid income response")
	}
	for _, row := range rows {
		if row.IncomeType != "FUNDING_FEE" {
			return fmt.Errorf("unexpected income type")
		}
	}
	return nil
}

func endpointName(path string) string {
	switch path {
	case "/fapi/v3/agent":
		return "agent-list"
	case "/fapi/v3/accountWithJoinMargin":
		return "account"
	case "/fapi/v3/positionRisk":
		return "position-risk"
	case "/fapi/v3/income":
		return "funding-income"
	default:
		return "private"
	}
}

func abs(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
