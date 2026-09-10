package dataagent

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const keyVersion = 1

type Status string

const (
	StatusPending    Status = "pending"
	StatusSubmitting Status = "submitting"
	StatusApproved   Status = "approved"
	StatusRejected   Status = "rejected"
	StatusUncertain  Status = "uncertain"
)

var (
	ErrNotFound      = errors.New("Aster data-agent probe not found")
	errStateConflict = errors.New("Aster data-agent state conflict")
)

type Record struct {
	ProbeID         string
	Owner           string
	ExecutionAgent  string
	AgentAddress    string
	PrivateKey      []byte
	ApprovalNonce   int64
	RequestedExpiry int64
	Status          Status
	ApprovedAt      *time.Time
	LastResult      *Report
	LastError       string
}

type ProbeStatus struct {
	Status          Status  `json:"status"`
	AgentAddress    string  `json:"agent_address"`
	RequestedExpiry int64   `json:"requested_expiry"`
	LastResult      *Report `json:"last_result,omitempty"`
	LastError       string  `json:"last_error,omitempty"`
}

type Store struct {
	db   *sql.DB
	aead cipher.AEAD
}

func NewStore(db *sql.DB, masterKey []byte) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("Aster data-agent database is required")
	}
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("ASTER_DATA_AGENT_MASTER_KEY must decode to exactly 32 bytes")
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("initialize Aster data-agent encryption: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize Aster data-agent authenticated encryption: %w", err)
	}
	if _, err := db.Exec(`UPDATE aster_data_agents
		SET status = 'uncertain', last_error = 'Aster data-agent approval was interrupted; outcome is uncertain; do not retry'
		WHERE status = 'submitting'`); err != nil {
		return nil, fmt.Errorf("recover interrupted Aster data-agent submissions: %w", err)
	}
	return &Store{db: db, aead: aead}, nil
}

// SavePending inserts a probe or replaces only a pending probe older than replaceBeforeNonce.
func (s *Store) SavePending(ctx context.Context, record Record, replaceBeforeNonce int64) error {
	record.Owner = strings.ToLower(record.Owner)
	record.ExecutionAgent = strings.ToLower(record.ExecutionAgent)
	record.AgentAddress = strings.ToLower(record.AgentAddress)
	record.Status = StatusPending
	if record.ProbeID == "" || record.Owner == "" || record.ExecutionAgent == "" ||
		record.AgentAddress == "" || len(record.PrivateKey) != 32 || record.ApprovalNonce <= 0 || record.RequestedExpiry <= 0 {
		return fmt.Errorf("invalid Aster data-agent record")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generate Aster data-agent encryption nonce: %w", err)
	}
	ciphertext := s.aead.Seal(nil, nonce, record.PrivateKey, recordAAD(record))
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO aster_data_agents (
			probe_id, owner_account, execution_agent, agent_address, key_version,
			key_nonce, key_ciphertext, approval_nonce, requested_expiry, status,
			approved_at, last_result_json, last_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', NULL, '', '')
		ON CONFLICT (owner_account) DO UPDATE SET
			probe_id = excluded.probe_id,
			execution_agent = excluded.execution_agent,
			agent_address = excluded.agent_address,
			key_version = excluded.key_version,
			key_nonce = excluded.key_nonce,
			key_ciphertext = excluded.key_ciphertext,
			approval_nonce = excluded.approval_nonce,
			requested_expiry = excluded.requested_expiry,
			status = 'pending',
			approved_at = NULL,
			last_result_json = '',
			last_error = ''
		WHERE aster_data_agents.status = 'pending' AND aster_data_agents.approval_nonce < ?`,
		record.ProbeID, record.Owner, record.ExecutionAgent, record.AgentAddress, keyVersion,
		nonce, ciphertext, record.ApprovalNonce, record.RequestedExpiry, replaceBeforeNonce,
	)
	if err != nil {
		return fmt.Errorf("save Aster data-agent probe: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errStateConflict
	}
	return nil
}

func (s *Store) Load(ctx context.Context, probeID string) (Record, error) {
	return s.load(ctx, `probe_id = ?`, probeID)
}

func (s *Store) LoadForOwner(ctx context.Context, probeID, owner string) (Record, error) {
	return s.load(ctx, `probe_id = ? AND owner_account = ?`, probeID, strings.ToLower(owner))
}

func (s *Store) LoadApprovedByOwner(ctx context.Context, owner string) (Record, error) {
	return s.load(ctx, `owner_account = ? AND status = 'approved'`, strings.ToLower(owner))
}

func (s *Store) LoadMetadata(ctx context.Context, probeID string) (Record, error) {
	return s.loadMetadata(ctx, `probe_id = ?`, probeID)
}

func (s *Store) StatusByOwner(ctx context.Context, owner string) (ProbeStatus, error) {
	record, err := s.loadMetadata(ctx, `owner_account = ?`, strings.ToLower(owner))
	if err != nil {
		return ProbeStatus{}, err
	}
	return ProbeStatus{
		Status: record.Status, AgentAddress: record.AgentAddress, RequestedExpiry: record.RequestedExpiry,
		LastResult: record.LastResult, LastError: record.LastError,
	}, nil
}

func (s *Store) BeginSubmission(ctx context.Context, probeID string) (Record, error) {
	row := s.db.QueryRowContext(ctx, `UPDATE aster_data_agents
		SET status = 'submitting', last_error = ''
		WHERE probe_id = ? AND status = 'pending'
		RETURNING probe_id, owner_account, execution_agent, agent_address,
			approval_nonce, requested_expiry, status, approved_at, last_result_json, last_error`, probeID)
	record, err := scanMetadata(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, errStateConflict
	}
	if err != nil {
		return Record{}, fmt.Errorf("begin Aster data-agent submission: %w", err)
	}
	return record, nil
}

func (s *Store) Transition(ctx context.Context, probeID string, from, to Status, publicError string, at time.Time) error {
	var approvedAt any
	if to == StatusApproved {
		approvedAt = at.UTC().Format(time.RFC3339Nano)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE aster_data_agents
		SET status = ?, approved_at = COALESCE(?, approved_at), last_error = ?
		WHERE probe_id = ? AND status = ?`, to, approvedAt, publicError, probeID, from)
	if err != nil {
		return fmt.Errorf("transition Aster data-agent status: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errStateConflict
	}
	return nil
}

func (s *Store) SaveResult(ctx context.Context, probeID string, report Report) error {
	encoded, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode Aster data-agent result: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE aster_data_agents
		SET last_result_json = ?, last_error = ? WHERE probe_id = ? AND status = 'approved'`,
		string(encoded), report.Error, probeID)
	if err != nil {
		return fmt.Errorf("save Aster data-agent result: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errStateConflict
	}
	return nil
}

func (s *Store) load(ctx context.Context, where string, args ...any) (Record, error) {
	query := `SELECT probe_id, owner_account, execution_agent, agent_address, key_version,
		key_nonce, key_ciphertext, approval_nonce, requested_expiry, status,
		approved_at, last_result_json, last_error FROM aster_data_agents WHERE ` + where
	var record Record
	var version int
	var nonce, ciphertext []byte
	var approvedAt sql.NullString
	var lastResult string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&record.ProbeID, &record.Owner, &record.ExecutionAgent, &record.AgentAddress, &version,
		&nonce, &ciphertext, &record.ApprovalNonce, &record.RequestedExpiry, &record.Status,
		&approvedAt, &lastResult, &record.LastError,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("load Aster data-agent probe: %w", err)
	}
	if version != keyVersion || len(nonce) != s.aead.NonceSize() {
		return Record{}, fmt.Errorf("unsupported or invalid Aster data-agent ciphertext")
	}
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, recordAAD(record))
	if err != nil || len(plaintext) != 32 {
		return Record{}, fmt.Errorf("authenticate Aster data-agent ciphertext")
	}
	record.PrivateKey = plaintext
	if err := applyMutableMetadata(&record, approvedAt, lastResult); err != nil {
		clear(plaintext)
		return Record{}, err
	}
	return record, nil
}

func (s *Store) loadMetadata(ctx context.Context, where string, args ...any) (Record, error) {
	query := `SELECT probe_id, owner_account, execution_agent, agent_address,
		approval_nonce, requested_expiry, status, approved_at, last_result_json, last_error
		FROM aster_data_agents WHERE ` + where
	record, err := scanMetadata(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("load Aster data-agent metadata: %w", err)
	}
	return record, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanMetadata(row rowScanner) (Record, error) {
	var record Record
	var approvedAt sql.NullString
	var lastResult string
	err := row.Scan(
		&record.ProbeID, &record.Owner, &record.ExecutionAgent, &record.AgentAddress,
		&record.ApprovalNonce, &record.RequestedExpiry, &record.Status, &approvedAt,
		&lastResult, &record.LastError,
	)
	if err != nil {
		return Record{}, err
	}
	if err := applyMutableMetadata(&record, approvedAt, lastResult); err != nil {
		return Record{}, err
	}
	return record, nil
}

func applyMutableMetadata(record *Record, approvedAt sql.NullString, lastResult string) error {
	if approvedAt.Valid {
		parsed, err := time.Parse(time.RFC3339Nano, approvedAt.String)
		if err != nil {
			return fmt.Errorf("invalid Aster data-agent approval timestamp")
		}
		record.ApprovedAt = &parsed
	}
	if lastResult != "" {
		var report Report
		if json.Unmarshal([]byte(lastResult), &report) != nil {
			return fmt.Errorf("invalid Aster data-agent stored result")
		}
		record.LastResult = &report
	}
	return nil
}

func recordAAD(record Record) []byte {
	return []byte(
		"version=" + strconv.Itoa(keyVersion) +
			"\nprobe_id=" + record.ProbeID +
			"\nowner=" + strings.ToLower(record.Owner) +
			"\nexecution_agent=" + strings.ToLower(record.ExecutionAgent) +
			"\nagent_address=" + strings.ToLower(record.AgentAddress) +
			"\napproval_nonce=" + strconv.FormatInt(record.ApprovalNonce, 10) +
			"\nrequested_expiry=" + strconv.FormatInt(record.RequestedExpiry, 10),
	)
}
