package persistence

import (
	"database/sql"
	"encoding/json"
	"errors"
	"ground-clock-qualification/internal/audit"
	"ground-clock-qualification/internal/domain"
	_ "modernc.org/sqlite"
	"sync"
)

var ErrResourceConflict = errors.New("resource conflict")

type Store struct {
	db *sql.DB
	mu sync.Mutex
}

func Open(path string) (*Store, error) {
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return nil, e
	}
	s := &Store{db: db}
	_, e = db.Exec(`CREATE TABLE IF NOT EXISTS campaigns(id TEXT PRIMARY KEY,data BLOB NOT NULL);
		CREATE TABLE IF NOT EXISTS refs(id TEXT PRIMARY KEY,campaign_id TEXT,data BLOB);
		CREATE INDEX IF NOT EXISTS refs_campaign ON refs(campaign_id);
		CREATE TABLE IF NOT EXISTS rounds(id TEXT PRIMARY KEY,campaign_id TEXT,seq INTEGER,data BLOB,UNIQUE(campaign_id,seq));
		CREATE TABLE IF NOT EXISTS deviations(id TEXT PRIMARY KEY,campaign_id TEXT,data BLOB);
		CREATE TABLE IF NOT EXISTS evaluations(campaign_id TEXT,input_summary TEXT,data BLOB,PRIMARY KEY(campaign_id,input_summary));
		CREATE TABLE IF NOT EXISTS reviews(campaign_id TEXT,revision INTEGER,data BLOB,PRIMARY KEY(campaign_id,revision));
		CREATE TABLE IF NOT EXISTS artifacts(campaign_id TEXT PRIMARY KEY,data BLOB);
		CREATE TABLE IF NOT EXISTS idem(request_id TEXT PRIMARY KEY,response BLOB,request_hash TEXT);
		CREATE TABLE IF NOT EXISTS audit(campaign_id TEXT,revision INTEGER PRIMARY KEY,data BLOB);
		CREATE TABLE IF NOT EXISTS audit_events(campaign_id TEXT,revision INTEGER,data BLOB,PRIMARY KEY(campaign_id,revision));
		CREATE TABLE IF NOT EXISTS remediation_plans(campaign_id TEXT,deviation_id TEXT,version INTEGER,data BLOB,PRIMARY KEY(campaign_id,deviation_id,version));
		CREATE TABLE IF NOT EXISTS round_voids(campaign_id TEXT,round_id TEXT PRIMARY KEY,data BLOB);
		CREATE INDEX IF NOT EXISTS round_voids_campaign ON round_voids(campaign_id);
		CREATE TABLE IF NOT EXISTS review_claims(campaign_id TEXT,version INTEGER,data BLOB,PRIMARY KEY(campaign_id,version));
		CREATE TABLE IF NOT EXISTS reference_withdrawals(campaign_id TEXT,evidence_id TEXT PRIMARY KEY,data BLOB);
		CREATE INDEX IF NOT EXISTS reference_withdrawals_campaign ON reference_withdrawals(campaign_id);
		CREATE TABLE IF NOT EXISTS review_findings(id TEXT PRIMARY KEY,campaign_id TEXT,data BLOB);
		CREATE INDEX IF NOT EXISTS review_findings_campaign ON review_findings(campaign_id);
		CREATE TABLE IF NOT EXISTS finding_resolutions(finding_id TEXT PRIMARY KEY,campaign_id TEXT,data BLOB);
		CREATE INDEX IF NOT EXISTS finding_resolutions_campaign ON finding_resolutions(campaign_id);
		CREATE TABLE IF NOT EXISTS device_baselines(campaign_id TEXT,device_id TEXT,data BLOB,PRIMARY KEY(campaign_id,device_id));
		CREATE TABLE IF NOT EXISTS sample_exclusions(campaign_id TEXT,round_id TEXT,device_id TEXT,data BLOB,PRIMARY KEY(campaign_id,round_id,device_id));
		CREATE TABLE IF NOT EXISTS remediation_evidence(id TEXT PRIMARY KEY,campaign_id TEXT,data BLOB);
		CREATE INDEX IF NOT EXISTS remediation_evidence_campaign ON remediation_evidence(campaign_id);
		CREATE TABLE IF NOT EXISTS review_snapshots(campaign_id TEXT,revision INTEGER,data BLOB,PRIMARY KEY(campaign_id,revision));
		CREATE TABLE IF NOT EXISTS remediation_dependencies(campaign_id TEXT,version INTEGER,deviation_id TEXT,prerequisite_id TEXT,data BLOB,PRIMARY KEY(campaign_id,version,deviation_id,prerequisite_id));
		CREATE INDEX IF NOT EXISTS remediation_dependencies_campaign ON remediation_dependencies(campaign_id,version);
		INSERT OR IGNORE INTO audit_events(campaign_id,revision,data) SELECT campaign_id,revision,data FROM audit;`)
	if e != nil {
		return nil, e
	}
	return s, nil
}

func (s *Store) DeviceBaselines(cid string) ([]domain.DeviceBaseline, error) {
	rows, err := s.db.Query(`SELECT data FROM device_baselines WHERE campaign_id=? ORDER BY device_id`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.DeviceBaseline{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var v domain.DeviceBaseline
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) SampleExclusions(cid string) ([]domain.SampleExclusion, error) {
	rows, err := s.db.Query(`SELECT data FROM sample_exclusions WHERE campaign_id=? ORDER BY round_id,device_id`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.SampleExclusion{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var v domain.SampleExclusion
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) RemediationEvidence(cid string) ([]domain.RemediationEvidence, error) {
	rows, err := s.db.Query(`SELECT data FROM remediation_evidence WHERE campaign_id=? ORDER BY json_extract(data,'$.deviation_id'),json_extract(data,'$.occurred_at'),id`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.RemediationEvidence{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var v domain.RemediationEvidence
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) ReviewSnapshot(cid string, rev int64) (*domain.ReviewSnapshot, error) {
	var b []byte
	err := s.db.QueryRow(`SELECT data FROM review_snapshots WHERE campaign_id=? AND revision=?`, cid, rev).Scan(&b)
	if err != nil {
		return nil, err
	}
	var v domain.ReviewSnapshot
	err = json.Unmarshal(b, &v)
	return &v, err
}

func (s *Store) ReferenceWithdrawals(cid string) ([]domain.ReferenceWithdrawal, error) {
	rows, err := s.db.Query(`SELECT data FROM reference_withdrawals WHERE campaign_id=? ORDER BY json_extract(data,'$.revision'),evidence_id`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ReferenceWithdrawal{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var v domain.ReferenceWithdrawal
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) ReviewFindings(cid string) ([]domain.ReviewFinding, error) {
	rows, err := s.db.Query(`SELECT data FROM review_findings WHERE campaign_id=? ORDER BY json_extract(data,'$.review_revision'),id`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ReviewFinding{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var v domain.ReviewFinding
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) FindingResolutions(cid string) ([]domain.FindingResolution, error) {
	rows, err := s.db.Query(`SELECT data FROM finding_resolutions WHERE campaign_id=? ORDER BY json_extract(data,'$.revision'),finding_id`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.FindingResolution{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var v domain.FindingResolution
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) RoundVoids(cid string) ([]domain.RoundVoid, error) {
	rows, err := s.db.Query(`SELECT data FROM round_voids WHERE campaign_id=? ORDER BY json_extract(data,'$.revision'),round_id`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.RoundVoid{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var v domain.RoundVoid
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) ReviewClaims(cid string) ([]domain.ReviewClaim, error) {
	rows, err := s.db.Query(`SELECT data FROM review_claims WHERE campaign_id=? ORDER BY version`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ReviewClaim{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var v domain.ReviewClaim
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) CurrentReviewClaim(cid string) (*domain.ReviewClaim, error) {
	var b []byte
	err := s.db.QueryRow(`SELECT data FROM review_claims WHERE campaign_id=? ORDER BY version DESC LIMIT 1`, cid).Scan(&b)
	if err != nil {
		return nil, err
	}
	var v domain.ReviewClaim
	if err = json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func (s *Store) Evaluations(cid string) ([]domain.Evaluation, error) {
	rows, e := s.db.Query(`SELECT data FROM evaluations WHERE campaign_id=? ORDER BY json_extract(data,'$.revision')`, cid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Evaluation{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var v domain.Evaluation
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Plans(cid string) ([]domain.RemediationPlan, error) {
	rows, e := s.db.Query(`SELECT data FROM remediation_plans WHERE campaign_id=? ORDER BY deviation_id,version`, cid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.RemediationPlan{}
	for rows.Next() {
		var b []byte
		rows.Scan(&b)
		var p domain.RemediationPlan
		json.Unmarshal(b, &p)
		out = append(out, p)
	}
	return out, rows.Err()
}

// CreateCampaignAtomic serializes the overlap check and the initial aggregate,
// audit and idempotency insert, which is also correct for SQLite's writer model.
func (s *Store) CreateCampaignAtomic(c *domain.Campaign, event audit.Event, key, hash string) ([]domain.ResourceConflict, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	rollback := func(err error) ([]domain.ResourceConflict, error) { tx.Rollback(); return nil, err }
	rows, e := tx.Query(`SELECT data FROM campaigns`)
	if e != nil {
		return rollback(e)
	}
	var existing []*domain.Campaign
	for rows.Next() {
		var b []byte
		rows.Scan(&b)
		var x domain.Campaign
		if json.Unmarshal(b, &x) == nil {
			if x.CampaignID == c.CampaignID {
				rows.Close()
				tx.Rollback()
				return nil, domain.ErrAlreadyExists
			}
			existing = append(existing, &x)
		}
	}
	rows.Close()
	conflicts := domain.WindowConflicts(c, existing)
	if len(conflicts) > 0 {
		tx.Rollback()
		return conflicts, ErrResourceConflict
	}
	cb, _ := json.Marshal(c)
	if _, e = tx.Exec(`INSERT INTO campaigns(id,data) VALUES(?,?)`, c.CampaignID, cb); e != nil {
		return rollback(e)
	}
	eb, _ := json.Marshal(event)
	if _, e = tx.Exec(`INSERT INTO audit_events(campaign_id,revision,data) VALUES(?,?,?)`, event.CampaignID, event.Revision, eb); e != nil {
		return rollback(e)
	}
	if key != "" {
		rb, _ := json.Marshal(c)
		if _, e = tx.Exec(`INSERT INTO idem(request_id,response,request_hash) VALUES(?,?,?)`, key, rb, hash); e != nil {
			return rollback(e)
		}
	}
	return nil, tx.Commit()
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) SaveCampaign(c *domain.Campaign) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(c)
	_, e := s.db.Exec(`INSERT INTO campaigns(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, c.CampaignID, b)
	return e
}
func (s *Store) GetCampaign(id string) (*domain.Campaign, error) {
	var b []byte
	e := s.db.QueryRow(`SELECT data FROM campaigns WHERE id=?`, id).Scan(&b)
	if e != nil {
		return nil, e
	}
	var c domain.Campaign
	e = json.Unmarshal(b, &c)
	return &c, e
}
func (s *Store) ListCampaigns() ([]*domain.Campaign, error) {
	rows, err := s.db.Query(`SELECT data FROM campaigns ORDER BY json_extract(data,'$.created_at'), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Campaign
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var c domain.Campaign
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}
func (s *Store) SaveReference(r domain.ReferenceEvidence) error {
	b, _ := json.Marshal(r)
	_, e := s.db.Exec(`INSERT OR REPLACE INTO refs(id,campaign_id,data) VALUES(?,?,?)`, r.EvidenceID, r.CampaignID, b)
	return e
}
func (s *Store) References(cid string) ([]domain.ReferenceEvidence, error) {
	rows, e := s.db.Query(`SELECT data FROM refs WHERE campaign_id=? ORDER BY id`, cid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.ReferenceEvidence
	for rows.Next() {
		var b []byte
		rows.Scan(&b)
		var r domain.ReferenceEvidence
		json.Unmarshal(b, &r)
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) SaveRound(r domain.MeasurementRound) error {
	b, _ := json.Marshal(r)
	_, e := s.db.Exec(`INSERT INTO rounds(id,campaign_id,seq,data) VALUES(?,?,?,?)`, r.RoundID, r.CampaignID, r.Sequence, b)
	return e
}
func (s *Store) SaveRounds(rs []domain.MeasurementRound) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	for _, r := range rs {
		b, _ := json.Marshal(r)
		if _, e = tx.Exec(`INSERT INTO rounds(id,campaign_id,seq,data) VALUES(?,?,?,?)`, r.RoundID, r.CampaignID, r.Sequence, b); e != nil {
			tx.Rollback()
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) Rounds(cid string) ([]domain.MeasurementRound, error) {
	rows, e := s.db.Query(`SELECT data FROM rounds WHERE campaign_id=? ORDER BY seq`, cid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.MeasurementRound
	for rows.Next() {
		var b []byte
		rows.Scan(&b)
		var r domain.MeasurementRound
		json.Unmarshal(b, &r)
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) SaveDeviation(d domain.DeviationCase) error {
	b, _ := json.Marshal(d)
	_, e := s.db.Exec(`INSERT OR REPLACE INTO deviations(id,campaign_id,data) VALUES(?,?,?)`, d.DeviationID, d.CampaignID, b)
	return e
}
func (s *Store) Deviations(cid string) ([]domain.DeviationCase, error) {
	rows, e := s.db.Query(`SELECT data FROM deviations WHERE campaign_id=? ORDER BY id`, cid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.DeviationCase
	for rows.Next() {
		var b []byte
		rows.Scan(&b)
		var d domain.DeviationCase
		json.Unmarshal(b, &d)
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) SaveArtifact(a domain.Artifact) error {
	b, _ := json.Marshal(a)
	_, e := s.db.Exec(`INSERT OR REPLACE INTO artifacts(campaign_id,data) VALUES(?,?)`, a.CampaignID, b)
	return e
}
func (s *Store) GetArtifact(cid string) (*domain.Artifact, error) {
	var b []byte
	e := s.db.QueryRow(`SELECT data FROM artifacts WHERE campaign_id=?`, cid).Scan(&b)
	if e != nil {
		return nil, e
	}
	var a domain.Artifact
	e = json.Unmarshal(b, &a)
	return &a, e
}
func (s *Store) PutIdem(id string, v any) error {
	b, _ := json.Marshal(v)
	_, e := s.db.Exec(`INSERT OR REPLACE INTO idem(request_id,response) VALUES(?,?)`, id, b)
	return e
}
func (s *Store) GetIdem(id string, v any) (bool, error) {
	var b []byte
	e := s.db.QueryRow(`SELECT response FROM idem WHERE request_id=?`, id).Scan(&b)
	if e == sql.ErrNoRows {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	return true, json.Unmarshal(b, v)
}
func (s *Store) GetIdemHash(id string) (string, bool, error) {
	var h string
	e := s.db.QueryRow(`SELECT request_hash FROM idem WHERE request_id=?`, id).Scan(&h)
	if e == sql.ErrNoRows {
		return "", false, nil
	}
	if e != nil {
		return "", false, e
	}
	return h, true, nil
}
func (s *Store) PutIdemHash(id, hash string, v any) error {
	b, _ := json.Marshal(v)
	_, e := s.db.Exec(`INSERT OR REPLACE INTO idem(request_id,response,request_hash) VALUES(?,?,?)`, id, b, hash)
	return e
}
func (s *Store) WithLock(fn func() error) error { s.mu.Lock(); defer s.mu.Unlock(); return fn() }

func (s *Store) SaveAudit(e audit.Event) error {
	b, _ := json.Marshal(e)
	_, err := s.db.Exec(`INSERT INTO audit_events(campaign_id,revision,data) VALUES(?,?,?)`, e.CampaignID, e.Revision, b)
	return err
}
func (s *Store) Audits(cid string) ([]audit.Event, error) {
	rows, err := s.db.Query(`SELECT data FROM audit_events WHERE campaign_id=? ORDER BY revision`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []audit.Event
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var e audit.Event
		if err := json.Unmarshal(b, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
