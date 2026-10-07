package ops

import (
	"communication-infrastructure/internal/provider"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"
)

const DemoWorkspace = "demo-workspace"
const resetLock int64 = 7426193

type Store struct {
	DNSTargets      map[string]string
	DB              *pgxpool.Pool
	Redis           *redis.Client
	ReplySuccess    atomic.Int64
	ReplyFailed     atomic.Bool
	Gateway         provider.Gateway
	ProviderLab     *provider.Lab
	IMessageBridge  *provider.BlueBubbles
	IMessageLab     *provider.IMessageLab
	CallbackOrigin  string
	SIPPeer         *provider.SIPPeer
	DeliverySuccess atomic.Int64
	DeliveryFailed  atomic.Bool
}

func ID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}
func Hash(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func Migrate(ctx context.Context, url, dir string) error {
	db, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer db.Close(ctx)
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(7426194)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(name text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz DEFAULT now())"); err != nil {
		return err
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return fmt.Errorf("no migrations found in %s", dir)
	}
	for _, p := range paths {
		data, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		name := filepath.Base(p)
		var checksum string
		e = tx.QueryRow(ctx, "SELECT checksum FROM schema_migrations WHERE name=$1", name).Scan(&checksum)
		if e == nil {
			if checksum != Hash(string(data)) {
				return fmt.Errorf("migration checksum changed: %s", name)
			}
			continue
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if _, e = tx.Exec(ctx, string(data)); e != nil {
			return fmt.Errorf("%s: %w", name, e)
		}
		if _, e = tx.Exec(ctx, "INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)", name, Hash(string(data))); e != nil {
			return e
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO workspaces(id,name) VALUES('demo-workspace','Synthetic operations') ON CONFLICT DO NOTHING;
 INSERT INTO users(id,workspace_id,name,role) VALUES
 ('demo-operator','demo-workspace','Demo operator','operator'),
 ('demo-viewer','demo-workspace','Demo reviewer','viewer') ON CONFLICT DO NOTHING;`)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Begin(ctx context.Context, workspace string, exclusive bool) (pgx.Tx, error) {
	return s.begin(ctx, workspace, exclusive, pgx.TxOptions{})
}
func (s *Store) BeginRead(ctx context.Context, workspace string) (pgx.Tx, error) {
	return s.begin(ctx, workspace, false, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
}
func (s *Store) begin(ctx context.Context, workspace string, exclusive bool, options pgx.TxOptions) (pgx.Tx, error) {
	tx, err := s.DB.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	lock := "SELECT pg_advisory_xact_lock_shared($1)"
	if exclusive {
		lock = "SELECT pg_advisory_xact_lock($1)"
	}
	if _, err = tx.Exec(ctx, lock, resetLock); err == nil {
		_, err = tx.Exec(ctx, "SELECT set_config('app.workspace_id',$1,true)", workspace)
	}
	if err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
func audit(ctx context.Context, tx pgx.Tx, u User, action string, asset, record, reason any) error {
	_, err := tx.Exec(ctx, "INSERT INTO audit(workspace_id,actor_id,actor_name,action,asset_id,record_id,reason) VALUES($1,$2,$3,$4,$5,$6,$7)", u.WorkspaceID, u.ID, u.Name, action, asset, record, reason)
	return err
}
func listAssets(ctx context.Context, tx pgx.Tx) ([]Asset, error) {
	rows, err := tx.Query(ctx, `SELECT id,kind,name,address,status,version,quarantined_at,quarantine_reason,email_config FROM assets ORDER BY kind DESC,name`)
	if err != nil {
		return nil, err
	}
	out := []Asset{}
	for rows.Next() {
		var a Asset
		var cfg []byte
		if err = rows.Scan(&a.ID, &a.Kind, &a.Name, &a.Address, &a.Status, &a.Version, &a.QuarantinedAt, &a.QuarantineReason, &cfg); err != nil {
			rows.Close()
			return nil, err
		}
		if cfg != nil {
			if err = json.Unmarshal(cfg, &a.EmailConfig); err != nil {
				rows.Close()
				return nil, err
			}
		}
		out = append(out, a)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		sample, e := latestSample(ctx, tx, out[i].ID)
		if e != nil {
			return nil, e
		}
		out[i].Sample = sample
	}
	return out, nil
}
func assetByID(ctx context.Context, tx pgx.Tx, id string, lock bool) (Asset, error) {
	q := `SELECT id,kind,name,address,status,version,quarantined_at,quarantine_reason,email_config FROM assets WHERE id=$1`
	if lock {
		q += " FOR UPDATE"
	}
	var a Asset
	var cfg []byte
	err := tx.QueryRow(ctx, q, id).Scan(&a.ID, &a.Kind, &a.Name, &a.Address, &a.Status, &a.Version, &a.QuarantinedAt, &a.QuarantineReason, &cfg)
	if err != nil {
		return a, err
	}
	if cfg != nil {
		err = json.Unmarshal(cfg, &a.EmailConfig)
	}
	if err == nil {
		a.Sample, err = latestSample(ctx, tx, id)
	}
	return a, err
}
func latestSample(ctx context.Context, tx pgx.Tx, id string) (*Sample, error) {
	var s Sample
	err := tx.QueryRow(ctx, `SELECT id,attempts,filtered,spam_label,observed_at,source FROM observations WHERE asset_id=$1 ORDER BY observed_at DESC,id DESC LIMIT 1`, id).Scan(&s.ID, &s.Attempts, &s.Filtered, &s.SpamLabel, &s.ObservedAt, &s.Source)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &s, err
}
func contactByID(ctx context.Context, tx pgx.Tx, id string, lock bool) (Contact, error) {
	q := `SELECT id,name,phone,email,dnc,opted_out_at,sms_consent_at,consent_source,warm_signal,warm_at,email_opened_at FROM contacts WHERE id=$1`
	if lock {
		q += " FOR UPDATE"
	}
	var c Contact
	err := tx.QueryRow(ctx, q, id).Scan(&c.ID, &c.Name, &c.Phone, &c.Email, &c.DNC, &c.OptedOutAt, &c.SMSConsentAt, &c.ConsentSource, &c.WarmSignal, &c.WarmAt, &c.EmailOpenedAt)
	return c, err
}
func decisions(ctx context.Context, tx pgx.Tx, channel, outcome, search string, limit, offset int) ([]Decision, int, error) {
	args := []any{channel, outcome, "%" + search + "%"}
	where := ` WHERE ($1='' OR d.channel=$1) AND ($2='' OR d.outcome=$2) AND (d.id ILIKE $3 OR d.reason ILIKE $3 OR d.contact_id ILIKE $3 OR coalesce(a.name,'') ILIKE $3) AND d.recorded_at>now()-interval '24 hours'`
	var total int
	err := tx.QueryRow(ctx, "SELECT count(*) FROM decisions d LEFT JOIN assets a ON a.workspace_id=d.workspace_id AND a.id=d.asset_id"+where, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := tx.Query(ctx, `SELECT d.id,d.contact_id,d.asset_id,a.name,d.channel,d.outcome,d.reason,d.evidence,d.recorded_at FROM decisions d LEFT JOIN assets a ON a.workspace_id=d.workspace_id AND a.id=d.asset_id`+where+` ORDER BY d.recorded_at DESC,d.id DESC LIMIT $4 OFFSET $5`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Decision{}
	for rows.Next() {
		var d Decision
		var raw []byte
		if err = rows.Scan(&d.ID, &d.ContactID, &d.AssetID, &d.AssetName, &d.Channel, &d.Outcome, &d.Reason, &raw, &d.RecordedAt); err != nil {
			return nil, 0, err
		}
		if err = json.Unmarshal(raw, &d.Evidence); err != nil {
			return nil, 0, err
		}
		d.ContactLabel = "Contact · " + d.ContactID
		out = append(out, d)
	}
	return out, total, rows.Err()
}
func auditList(ctx context.Context, tx pgx.Tx, asset string) ([]Audit, error) {
	rows, err := tx.Query(ctx, `SELECT id,actor_name,action,asset_id,record_id,reason,recorded_at FROM audit WHERE ($1='' OR asset_id=$1) ORDER BY recorded_at DESC,id DESC LIMIT 30`, asset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Audit{}
	for rows.Next() {
		var a Audit
		if err = rows.Scan(&a.ID, &a.ActorName, &a.Action, &a.AssetID, &a.RecordID, &a.Reason, &a.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func calls(ctx context.Context, tx pgx.Tx) ([]Call, error) {
	rows, err := tx.Query(ctx, `SELECT id,contact_id,asset_id,decision_id,status,outcome,started_at,completed_at FROM calls ORDER BY started_at DESC LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Call{}
	for rows.Next() {
		var c Call
		if err = rows.Scan(&c.ID, &c.ContactID, &c.AssetID, &c.DecisionID, &c.Status, &c.Outcome, &c.StartedAt, &c.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func deadline(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 5*time.Second)
}
