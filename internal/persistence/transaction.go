package persistence

import (
	"context"
	"encoding/json"
	"ground-clock-qualification/internal/audit"
	"ground-clock-qualification/internal/domain"
	"strings"
	"time"
)

type Mutation struct {
	Campaign                *domain.Campaign
	NewCampaign             bool
	References              []domain.ReferenceEvidence
	UpdateReferences        []domain.ReferenceEvidence
	Rounds                  []domain.MeasurementRound
	Deviations              []domain.DeviationCase
	Evaluation              *domain.Evaluation
	Review                  *domain.Review
	Artifact                *domain.Artifact
	Event                   *audit.Event
	IdemKey                 string
	IdemHash                string
	Response                any
	Plans                   []domain.RemediationPlan
	RoundVoids              []domain.RoundVoid
	ReviewClaims            []domain.ReviewClaim
	ReferenceWithdrawals    []domain.ReferenceWithdrawal
	ReviewFindings          []domain.ReviewFinding
	FindingResolutions      []domain.FindingResolution
	DeviceBaselines         []domain.DeviceBaseline
	SampleExclusions        []domain.SampleExclusion
	RemediationEvidence     []domain.RemediationEvidence
	ReviewSnapshot          *domain.ReviewSnapshot
	RemediationDependencies []domain.RemediationDependency
}

func encode(v any) ([]byte, error) { return json.Marshal(v) }

// Commit applies one aggregate revision. Inserts that represent evidence remain
// append-only; any constraint failure rolls the complete revision back.
func (s *Store) Commit(m Mutation) error {
	return s.CommitContext(context.Background(), m)
}

// CommitContext applies one aggregate revision like Commit, but checks the
// request context immediately before the irreversible transaction commit. If the
// context has been cancelled, the transaction is rolled back so that no partial
// revision, artifact or audit event is persisted, and the context error is
// returned. The check runs under the writer lock so no concurrent mutation can
// interleave between the check and the commit.
func (s *Store) CommitContext(ctx context.Context, m Mutation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	rollback := func(e error) error { _ = tx.Rollback(); return e }
	if m.Campaign != nil {
		b, e := encode(m.Campaign)
		if e != nil {
			return rollback(e)
		}
		statement := `INSERT INTO campaigns(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`
		if m.NewCampaign {
			statement = `INSERT INTO campaigns(id,data) VALUES(?,?)`
		}
		if _, e = tx.Exec(statement, m.Campaign.CampaignID, b); e != nil {
			return rollback(e)
		}
	}
	for _, ref := range m.References {
		rows, e := tx.Query(`SELECT refs.data FROM refs LEFT JOIN reference_withdrawals ON reference_withdrawals.campaign_id=refs.campaign_id AND reference_withdrawals.evidence_id=refs.id WHERE lower(json_extract(refs.data,'$.certificate_digest'))=lower(?) AND reference_withdrawals.evidence_id IS NULL`, ref.CertificateDigest)
		if e != nil {
			return rollback(e)
		}
		history := []domain.ReferenceEvidence{}
		for rows.Next() {
			var raw []byte
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				return rollback(e)
			}
			var item domain.ReferenceEvidence
			if e = json.Unmarshal(raw, &item); e != nil {
				rows.Close()
				return rollback(e)
			}
			history = append(history, item)
		}
		if e = rows.Close(); e != nil {
			return rollback(e)
		}
		if _, e = domain.CompareCertificateFingerprint(ref, history); e != nil {
			return rollback(e)
		}
		b, e := encode(ref)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO refs(id,campaign_id,data) VALUES(?,?,?)`, ref.EvidenceID, ref.CampaignID, b); e != nil {
			return rollback(e)
		}
	}
	for _, ref := range m.UpdateReferences {
		b, e := encode(ref)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`UPDATE refs SET data=? WHERE id=? AND campaign_id=?`, b, ref.EvidenceID, ref.CampaignID); e != nil {
			return rollback(e)
		}
	}
	for _, p := range m.Plans {
		b, e := encode(p)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO remediation_plans(campaign_id,deviation_id,version,data) VALUES(?,?,?,?)`, m.Campaign.CampaignID, p.DeviationID, p.Version, b); e != nil {
			return rollback(e)
		}
	}
	for _, round := range m.Rounds {
		b, e := encode(round)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO rounds(id,campaign_id,seq,data) VALUES(?,?,?,?)`, round.RoundID, round.CampaignID, round.Sequence, b); e != nil {
			return rollback(e)
		}
	}
	for _, item := range m.RoundVoids {
		b, e := encode(item)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO round_voids(campaign_id,round_id,data) VALUES(?,?,?)`, item.CampaignID, item.RoundID, b); e != nil {
			return rollback(e)
		}
	}
	for _, item := range m.ReviewClaims {
		b, e := encode(item)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO review_claims(campaign_id,version,data) VALUES(?,?,?)`, item.CampaignID, item.Version, b); e != nil {
			return rollback(e)
		}
	}
	for _, item := range m.ReferenceWithdrawals {
		b, e := encode(item)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO reference_withdrawals(campaign_id,evidence_id,data) VALUES(?,?,?)`, item.CampaignID, item.EvidenceID, b); e != nil {
			return rollback(e)
		}
	}
	for _, item := range m.ReviewFindings {
		b, e := encode(item)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO review_findings(id,campaign_id,data) VALUES(?,?,?)`, item.FindingID, item.CampaignID, b); e != nil {
			return rollback(e)
		}
	}
	for _, item := range m.FindingResolutions {
		b, e := encode(item)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO finding_resolutions(finding_id,campaign_id,data) VALUES(?,?,?)`, item.FindingID, m.Campaign.CampaignID, b); e != nil {
			return rollback(e)
		}
	}
	for _, item := range m.DeviceBaselines {
		b, e := encode(item)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO device_baselines(campaign_id,device_id,data) VALUES(?,?,?)`, item.CampaignID, item.DeviceID, b); e != nil {
			return rollback(e)
		}
	}
	for _, item := range m.SampleExclusions {
		b, e := encode(item)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO sample_exclusions(campaign_id,round_id,device_id,data) VALUES(?,?,?,?)`, item.CampaignID, item.RoundID, item.DeviceID, b); e != nil {
			return rollback(e)
		}
	}
	for _, item := range m.RemediationEvidence {
		b, e := encode(item)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO remediation_evidence(id,campaign_id,data) VALUES(?,?,?)`, item.EvidenceID, item.CampaignID, b); e != nil {
			return rollback(e)
		}
	}
	for _, item := range m.RemediationDependencies {
		b, e := encode(item)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO remediation_dependencies(campaign_id,version,deviation_id,prerequisite_id,data) VALUES(?,?,?,?,?)`, m.Campaign.CampaignID, item.Version, item.DeviationID, item.PrerequisiteDeviationID, b); e != nil {
			return rollback(e)
		}
	}
	if m.ReviewSnapshot != nil {
		b, e := encode(m.ReviewSnapshot)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO review_snapshots(campaign_id,revision,data) VALUES(?,?,?)`, m.ReviewSnapshot.CampaignID, m.ReviewSnapshot.Revision, b); e != nil {
			return rollback(e)
		}
	}
	for _, deviation := range m.Deviations {
		b, e := encode(deviation)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO deviations(id,campaign_id,data) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, deviation.DeviationID, deviation.CampaignID, b); e != nil {
			return rollback(e)
		}
	}
	if m.Evaluation != nil {
		b, e := encode(m.Evaluation)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO evaluations(campaign_id,input_summary,data) VALUES(?,?,?)`, m.Evaluation.CampaignID, m.Evaluation.InputSummary, b); e != nil {
			return rollback(e)
		}
	}
	if m.Review != nil {
		b, e := encode(m.Review)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO reviews(campaign_id,revision,data) VALUES(?,?,?)`, m.Campaign.CampaignID, m.Campaign.Revision, b); e != nil {
			return rollback(e)
		}
	}
	if m.Artifact != nil {
		b, e := encode(m.Artifact)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO artifacts(campaign_id,data) VALUES(?,?)`, m.Artifact.CampaignID, b); e != nil {
			return rollback(e)
		}
	}
	if m.Event != nil {
		b, e := encode(m.Event)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO audit_events(campaign_id,revision,data) VALUES(?,?,?)`, m.Event.CampaignID, m.Event.Revision, b); e != nil {
			return rollback(e)
		}
	}
	if m.IdemKey != "" {
		b, e := encode(m.Response)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO idem(request_id,response,request_hash) VALUES(?,?,?)`, m.IdemKey, b, m.IdemHash); e != nil {
			return rollback(e)
		}
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
	}
	return tx.Commit()
}

// CommitAmendment serializes the exclusion-aware resource check with the
// aggregate, audit event and idempotency result update.
func (s *Store) CommitAmendment(c *domain.Campaign, event audit.Event, key, hash string, response any) ([]domain.ResourceConflict, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	rollback := func(e error) ([]domain.ResourceConflict, error) { _ = tx.Rollback(); return nil, e }
	rows, err := tx.Query(`SELECT data FROM campaigns WHERE id<>?`, c.CampaignID)
	if err != nil {
		return rollback(err)
	}
	existing := []*domain.Campaign{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return rollback(err)
		}
		var item domain.Campaign
		if err = json.Unmarshal(b, &item); err != nil {
			rows.Close()
			return rollback(err)
		}
		existing = append(existing, &item)
	}
	if err = rows.Close(); err != nil {
		return rollback(err)
	}
	conflicts := domain.WindowConflicts(c, existing)
	if len(conflicts) > 0 {
		_ = tx.Rollback()
		return conflicts, ErrResourceConflict
	}
	cb, err := encode(c)
	if err != nil {
		return rollback(err)
	}
	if _, err = tx.Exec(`UPDATE campaigns SET data=? WHERE id=?`, cb, c.CampaignID); err != nil {
		return rollback(err)
	}
	eb, err := encode(event)
	if err != nil {
		return rollback(err)
	}
	if _, err = tx.Exec(`INSERT INTO audit_events(campaign_id,revision,data) VALUES(?,?,?)`, event.CampaignID, event.Revision, eb); err != nil {
		return rollback(err)
	}
	if key != "" {
		rb, e := encode(response)
		if e != nil {
			return rollback(e)
		}
		if _, e = tx.Exec(`INSERT INTO idem(request_id,response,request_hash) VALUES(?,?,?)`, key, rb, hash); e != nil {
			return rollback(e)
		}
	}
	return nil, tx.Commit()
}

type CampaignFilter struct {
	State, Station, ReviewerID string
	CancellationReason         string
	WindowStart, WindowEnd     *time.Time
	Offset, Limit              int
}

func (s *Store) QueryCampaigns(f CampaignFilter) ([]*domain.Campaign, int, error) {
	where := make([]string, 0, 4)
	args := make([]any, 0, 6)
	if f.State != "" {
		where = append(where, `json_extract(campaigns.data,'$.state')=?`)
		args = append(args, f.State)
	}
	if f.CancellationReason != "" {
		where = append(where, `json_extract(campaigns.data,'$.cancellation.reason_code')=?`)
		args = append(args, f.CancellationReason)
	}
	if f.Station != "" {
		where = append(where, `json_extract(campaigns.data,'$.station_code')=?`)
		args = append(args, f.Station)
	}
	if f.ReviewerID != "" {
		where = append(where, `json_extract(campaigns.data,'$.created_by')<>? AND NOT EXISTS (SELECT 1 FROM rounds WHERE rounds.campaign_id=campaigns.id AND json_extract(rounds.data,'$.operator_id')=?)`)
		args = append(args, f.ReviewerID, f.ReviewerID)
	}
	if f.WindowStart != nil {
		where = append(where, `json_extract(campaigns.data,'$.mission_window_start')>=?`)
		args = append(args, f.WindowStart.UTC().Format(time.RFC3339Nano))
	}
	if f.WindowEnd != nil {
		where = append(where, `json_extract(campaigns.data,'$.mission_window_end')<=?`)
		args = append(args, f.WindowEnd.UTC().Format(time.RFC3339Nano))
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRow("SELECT count(*) FROM campaigns AS campaigns"+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	queryArgs := append(append([]any(nil), args...), f.Limit, f.Offset)
	rows, err := s.db.Query("SELECT campaigns.data FROM campaigns AS campaigns"+clause+" ORDER BY json_extract(campaigns.data,'$.created_at'),campaigns.id LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]*domain.Campaign, 0)
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, 0, err
		}
		var c domain.Campaign
		if err = json.Unmarshal(b, &c); err != nil {
			return nil, 0, err
		}
		out = append(out, &c)
	}
	return out, total, rows.Err()
}

func (s *Store) Evaluation(cid, summary string) (*domain.Evaluation, error) {
	var b []byte
	err := s.db.QueryRow(`SELECT data FROM evaluations WHERE campaign_id=? AND input_summary=?`, cid, summary).Scan(&b)
	if err != nil {
		return nil, err
	}
	var out domain.Evaluation
	if err = json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) LatestEvaluation(cid string) (*domain.Evaluation, error) {
	var b []byte
	err := s.db.QueryRow(`SELECT data FROM evaluations WHERE campaign_id=? ORDER BY json_extract(data,'$.revision') DESC LIMIT 1`, cid).Scan(&b)
	if err != nil {
		return nil, err
	}
	var out domain.Evaluation
	if err = json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) Reviews(cid string) ([]domain.Review, error) {
	rows, err := s.db.Query(`SELECT data FROM reviews WHERE campaign_id=? ORDER BY revision`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Review, 0)
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var r domain.Review
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
