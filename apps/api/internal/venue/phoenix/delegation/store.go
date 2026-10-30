package delegation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("Phoenix delegation request not found")
	ErrBusy     = errors.New("Phoenix owner has an unresolved delegation request")
)

type Store struct{ writer, reader *sql.DB }

func NewStore(writer, reader *sql.DB) (*Store, error) {
	if writer == nil || reader == nil {
		return nil, errors.New("Phoenix delegation databases required")
	}
	return &Store{writer: writer, reader: reader}, nil
}

func (s *Store) load(ctx context.Context, owner, id string) (record, error) {
	var r record
	var raw string
	err := s.reader.QueryRowContext(ctx, `SELECT record_json,state,signature FROM phoenix_delegations WHERE owner_account=? AND (?='' OR request_id=?)`, owner, id, id).Scan(&raw, &r.State, &r.Signature)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	state, sig := r.State, r.Signature
	if err = json.Unmarshal([]byte(raw), &r); err != nil {
		return r, err
	}
	r.State, r.Signature = state, sig
	if r.Owner != owner || (id != "" && r.Request.ID != id) {
		return record{}, errors.New("Phoenix delegation record mismatch")
	}
	return r, nil
}

func (s *Store) savePrepared(ctx context.Context, r record) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	result, err := s.writer.ExecContext(ctx, `INSERT INTO phoenix_delegations(owner_account,request_id,record_json,state,signature,expires_unix_ns) VALUES(?,?,?,'prepared','',?)
	 ON CONFLICT(owner_account) DO UPDATE SET request_id=excluded.request_id,record_json=excluded.record_json,state='prepared',signature='',expires_unix_ns=excluded.expires_unix_ns
	 WHERE phoenix_delegations.state IN ('confirmed','failed','expired','superseded') OR (phoenix_delegations.state='prepared' AND phoenix_delegations.expires_unix_ns<=?)`, r.Owner, r.Request.ID, string(raw), r.Request.ExpiresAt.UnixNano(), time.Now().UnixNano())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrBusy
	}
	return nil
}

// transition persists one-shot submission/confirmation before reporting it.
// Authority registration and confirmation share the same SQLite transaction.
func (s *Store) transition(ctx context.Context, r record, from, to, signature string) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE phoenix_delegations SET state=?,signature=? WHERE owner_account=? AND request_id=? AND state=?`, to, signature, r.Owner, r.Request.ID, from)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrBusy
	}
	if to == "confirmed" {
		if r.Target == r.Owner {
			_, err = tx.ExecContext(ctx, `DELETE FROM agent_authorizations WHERE venue='phoenix' AND owner_account=?`, r.Owner)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO agent_authorizations(venue,owner_account,agent_address,authorized_at) VALUES('phoenix',?,?,?) ON CONFLICT(venue,owner_account) DO UPDATE SET agent_address=excluded.agent_address,authorized_at=excluded.authorized_at`, r.Owner, r.Target, time.Now().UTC().Format(time.RFC3339Nano))
		}
	} else if to == "superseded" {
		_, err = tx.ExecContext(ctx, `DELETE FROM agent_authorizations WHERE venue='phoenix' AND owner_account=? AND agent_address=?`, r.Owner, r.Target)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
