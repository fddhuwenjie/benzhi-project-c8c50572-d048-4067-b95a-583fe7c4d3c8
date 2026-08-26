package application

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"ground-clock-qualification/internal/audit"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	"math"
	"sort"
	"strings"
	"time"
)

type BaselineRegistration struct {
	Baselines        []domain.DeviceBaseline `json:"baselines"`
	DeviceBaselines  []domain.DeviceBaseline `json:"device_baselines,omitempty"`
	ExpectedRevision int64                   `json:"expected_revision"`
}
type BaselineResult struct {
	Campaign  *domain.Campaign        `json:"campaign"`
	Baselines []domain.DeviceBaseline `json:"baselines"`
}

func (s *Service) RegisterDeviceBaselines(id, requestID string, in BaselineRegistration) (*BaselineResult, error) {
	c, e := s.get(id)
	if e != nil {
		return nil, e
	}
	if strings.TrimSpace(requestID) == "" {
		return nil, domain.ErrInvalid
	}
	hash, key := requestHash(in), idemKey("device-baselines", id, requestID)
	var old BaselineResult
	if ok, e := s.replay(key, hash, &old); ok || e != nil {
		return &old, e
	}
	if e = s.check(c, in.ExpectedRevision); e != nil {
		return nil, e
	}
	if c.State != domain.Draft {
		return nil, domain.ErrState
	}
	if len(in.Baselines) == 0 {
		in.Baselines = in.DeviceBaselines
	}
	if len(in.Baselines) == 0 {
		return nil, domain.ErrInvalid
	}
	existing, e := s.Store.DeviceBaselines(id)
	if e != nil {
		return nil, e
	}
	if len(existing) > 0 {
		return nil, domain.ErrState
	}
	refs, e := s.Store.References(id)
	if e != nil {
		return nil, e
	}
	rounds, e := s.Store.Rounds(id)
	if e != nil {
		return nil, e
	}
	if len(refs) > 0 || len(rounds) > 0 {
		return nil, domain.ErrState
	}
	seenD, seenS := map[string]bool{}, map[string]bool{}
	by := map[string]bool{}
	for _, d := range c.DeviceIDs {
		by[d] = true
	}
	out := make([]domain.DeviceBaseline, 0, len(in.Baselines))
	now := time.Now().UTC()
	for _, b := range in.Baselines {
		b.CampaignID = id
		b.DeviceID = strings.TrimSpace(b.DeviceID)
		b.DeviceType = strings.TrimSpace(b.DeviceType)
		b.AssetSerial = strings.TrimSpace(b.AssetSerial)
		b.FirmwareVersion = strings.TrimSpace(b.FirmwareVersion)
		b.RegisteredBy = strings.TrimSpace(b.RegisteredBy)
		if !by[b.DeviceID] || seenD[b.DeviceID] || seenS[strings.ToLower(b.AssetSerial)] || b.DeviceType == "" || b.AssetSerial == "" || b.FirmwareVersion == "" || b.RegisteredBy == "" || b.CalibrationValidUntil.Before(c.MissionWindowEnd) || b.CalibrationValidUntil.IsZero() || (!b.CalibrationValidFrom.IsZero() && b.CalibrationValidFrom.After(c.MissionWindowStart)) {
			return nil, domain.ErrInvalid
		}
		seenD[b.DeviceID] = true
		seenS[strings.ToLower(b.AssetSerial)] = true
		b.RegisteredAt = now
		out = append(out, b)
	}
	if len(out) != len(c.DeviceIDs) {
		return nil, domain.ErrInvalid
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeviceID < out[j].DeviceID })
	c.Revision++
	for i := range out {
		out[i].Revision = c.Revision
	}
	result := &BaselineResult{c, out}
	ev, e := s.newEvent(c, "DEVICE_BASELINES_REGISTERED", out[0].RegisteredBy, requestHash(out))
	if e != nil {
		return nil, e
	}
	e = s.Store.Commit(persistence.Mutation{Campaign: c, DeviceBaselines: out, Event: &ev, IdemKey: key, IdemHash: hash, Response: result})
	return result, e
}

type ReferenceBatch struct {
	References       []domain.ReferenceEvidence `json:"references"`
	Evidence         []domain.ReferenceEvidence `json:"evidence,omitempty"`
	ExpectedRevision int64                      `json:"expected_revision"`
}
type ReferenceBatchResult struct {
	Campaign          *domain.Campaign           `json:"campaign"`
	References        []domain.ReferenceEvidence `json:"references"`
	Coverage          domain.ReferenceCoverage   `json:"coverage"`
	SourceCampaignIDs []string                   `json:"source_campaign_ids"`
}

func (s *Service) ConfirmReferenceBatch(id, requestID string, in ReferenceBatch) (*ReferenceBatchResult, error) {
	c, e := s.get(id)
	if e != nil {
		return nil, e
	}
	if strings.TrimSpace(requestID) == "" {
		return nil, domain.ErrInvalid
	}
	hash, key := requestHash(in), idemKey("reference-batch", id, requestID)
	var old ReferenceBatchResult
	if ok, e := s.replay(key, hash, &old); ok || e != nil {
		return &old, e
	}
	if e = s.check(c, in.ExpectedRevision); e != nil {
		return nil, e
	}
	if len(in.References) == 0 {
		in.References = in.Evidence
	}
	if c.State != domain.Draft || len(in.References) == 0 {
		return nil, domain.ErrState
	}
	refs, e := s.Store.References(id)
	if e != nil {
		return nil, e
	}
	w, e := s.Store.ReferenceWithdrawals(id)
	if e != nil {
		return nil, e
	}
	active := domain.EffectiveReferences(refs, w)
	seen := map[string]bool{}
	seenTuple := map[string]bool{}
	seenDigest := map[string]bool{}
	for _, r := range refs {
		seen[r.EvidenceID] = true
		seenDigest[strings.ToLower(r.CertificateDigest)] = true
		seenTuple[r.ReferenceKind+"|"+strings.ToLower(r.Provider)+"|"+r.ValidFrom.UTC().Format(time.RFC3339Nano)+"|"+r.ValidUntil.UTC().Format(time.RFC3339Nano)] = true
	}
	for _, r := range active {
		seenTuple[r.ReferenceKind+"|"+strings.ToLower(r.Provider)+"|"+r.ValidFrom.UTC().Format(time.RFC3339Nano)+"|"+r.ValidUntil.UTC().Format(time.RFC3339Nano)] = true
	}
	out := make([]domain.ReferenceEvidence, 0, len(in.References))
	now := time.Now().UTC()
	reuseCount := 0
	reuseCampaigns := []string{}
	for _, r := range in.References {
		r.CampaignID = id
		r.Provider = strings.TrimSpace(r.Provider)
		r.SubmittedBy = strings.TrimSpace(r.SubmittedBy)
		if r.SubmittedAt.IsZero() {
			r.SubmittedAt = now
		}
		if seen[r.EvidenceID] || r.EvidenceID == "" || r.Provider == "" || r.SubmittedBy == "" || r.ReferenceKind != "clock" && r.ReferenceKind != "frequency" || len(r.CertificateDigest) == 0 || len(r.CertificateDigest)%2 != 0 || !isHexDigest(r.CertificateDigest) || !r.ValidFrom.Before(r.ValidUntil) {
			return nil, domain.ErrInvalid
		}
		sources, sourceErr := s.certificateSources(r)
		if sourceErr != nil {
			return nil, sourceErr
		}
		reuseCount += len(sources)
		reuseCampaigns = append(reuseCampaigns, sources...)
		seen[r.EvidenceID] = true
		if seenDigest[strings.ToLower(r.CertificateDigest)] {
			return nil, domain.ErrDuplicate
		}
		for _, x := range out {
			if strings.EqualFold(x.CertificateDigest, r.CertificateDigest) {
				return nil, domain.ErrDuplicate
			}
		}
		tuple := r.ReferenceKind + "|" + strings.ToLower(r.Provider) + "|" + r.ValidFrom.UTC().Format(time.RFC3339Nano) + "|" + r.ValidUntil.UTC().Format(time.RFC3339Nano)
		if seenTuple[tuple] {
			return nil, domain.ErrDuplicate
		}
		seenTuple[tuple] = true
		seenDigest[strings.ToLower(r.CertificateDigest)] = true
		out = append(out, r)
	}
	coverage := c.ReferenceCoverage(append(active, out...))
	c.Revision++
	if coverage.Complete {
		c.State = domain.ReferenceVerified
	}
	sort.Strings(reuseCampaigns)
	result := &ReferenceBatchResult{Campaign: c, References: out, Coverage: coverage, SourceCampaignIDs: uniqueStrings(reuseCampaigns)}
	ev, e := s.newEvent(c, "REFERENCE_BATCH_CONFIRMED", out[0].SubmittedBy, fmt.Sprintf("count=%d coverage=%t fingerprint=%s source_campaign_count=%d", len(out), coverage.Complete, domain.CertificateFingerprint(out[0]), reuseCount))
	if e != nil {
		return nil, e
	}
	e = s.Store.Commit(persistence.Mutation{Campaign: c, References: out, Event: &ev, IdemKey: key, IdemHash: hash, Response: result})
	return result, e
}
func isHexDigest(v string) bool {
	if len(v)%2 != 0 {
		return false
	}
	_, e := hex.DecodeString(v)
	return e == nil
}

type SampleExclusionInput struct {
	RoundID          string `json:"round_id"`
	DeviceID         string `json:"device_id"`
	ReasonCode       string `json:"reason_code"`
	Reason           string `json:"reason"`
	ExcludedBy       string `json:"excluded_by"`
	OperatorID       string `json:"operator_id,omitempty"`
	ExpectedRevision int64  `json:"expected_revision"`
}
type SampleExclusionResult struct {
	Campaign  *domain.Campaign           `json:"campaign"`
	Exclusion domain.SampleExclusion     `json:"exclusion"`
	Coverage  domain.MeasurementCoverage `json:"coverage"`
}

func (s *Service) ExcludeSample(id, requestID string, in SampleExclusionInput) (*SampleExclusionResult, error) {
	c, e := s.get(id)
	if e != nil {
		return nil, e
	}
	if strings.TrimSpace(requestID) == "" {
		return nil, domain.ErrInvalid
	}
	hash, key := requestHash(in), idemKey("sample-exclusion", id, requestID)
	var old SampleExclusionResult
	if ok, e := s.replay(key, hash, &old); ok || e != nil {
		return &old, e
	}
	if e = s.check(c, in.ExpectedRevision); e != nil {
		return nil, e
	}
	if c.State != domain.Measured && c.State != domain.ReferenceVerified {
		return nil, domain.ErrState
	}
	if in.ExcludedBy == "" {
		in.ExcludedBy = in.OperatorID
	}
	if strings.TrimSpace(in.RoundID) == "" || strings.TrimSpace(in.DeviceID) == "" || !reasonCodePattern.MatchString(in.ReasonCode) || strings.TrimSpace(in.Reason) == "" || strings.TrimSpace(in.ExcludedBy) == "" {
		return nil, domain.ErrInvalid
	}
	if es, e := s.Store.Evaluations(id); e != nil {
		return nil, e
	} else if len(es) > 0 {
		return nil, domain.ErrState
	}
	rounds, e := s.Store.Rounds(id)
	if e != nil {
		return nil, e
	}
	found := false
	purpose := ""
	for _, r := range rounds {
		if r.RoundID == in.RoundID {
			purpose = r.Purpose
			for _, sm := range r.Samples {
				if sm.DeviceID == in.DeviceID {
					found = true
				}
			}
		}
	}
	if !found || purpose != "original" {
		return nil, domain.ErrInvalid
	}
	xs, e := s.Store.SampleExclusions(id)
	if e != nil {
		return nil, e
	}
	for _, x := range xs {
		if x.RoundID == in.RoundID && x.DeviceID == in.DeviceID {
			return nil, domain.ErrDuplicate
		}
	}
	voids, e := s.Store.RoundVoids(id)
	if e != nil {
		return nil, e
	}
	c.Revision++
	x := domain.SampleExclusion{CampaignID: id, RoundID: in.RoundID, DeviceID: in.DeviceID, ReasonCode: strings.TrimSpace(in.ReasonCode), Reason: strings.TrimSpace(in.Reason), ExcludedBy: strings.TrimSpace(in.ExcludedBy), ExcludedAt: time.Now().UTC(), Revision: c.Revision}
	effective := effectiveWithoutExclusions(rounds, voids, append(xs, x))
	comp, _ := domain.MeasurementPlanCompliance(c, effective)
	cov := domain.MeasurementReadinessFor(c, effective)
	cov.Complete = comp.Complete
	if cov.Complete {
		c.State = domain.Measured
	} else {
		c.State = domain.ReferenceVerified
	}
	result := &SampleExclusionResult{c, x, cov}
	ev, e := s.newEvent(c, "MEASUREMENT_SAMPLE_EXCLUDED", x.ExcludedBy, requestHash(x))
	if e != nil {
		return nil, e
	}
	e = s.Store.Commit(persistence.Mutation{Campaign: c, SampleExclusions: []domain.SampleExclusion{x}, Event: &ev, IdemKey: key, IdemHash: hash, Response: result})
	return result, e
}
func effectiveWithoutExclusions(rounds []domain.MeasurementRound, voids []domain.RoundVoid, xs []domain.SampleExclusion) []domain.MeasurementRound {
	return domain.EffectiveRoundsWithExclusions(rounds, voids, xs)
}

type ReproducibilityResult struct {
	Reproducible      bool             `json:"reproducible"`
	InputDigestMatch  bool             `json:"input_digest_match"`
	MetricDifferences []map[string]any `json:"metric_differences"`
	FailureCode       string           `json:"failure_code,omitempty"`
}

func (s *Service) VerifyReproducibility(id string, rev int64) (*ReproducibilityResult, error) {
	c, e := s.get(id)
	if e != nil {
		return nil, e
	}
	es, e := s.Store.Evaluations(id)
	if e != nil {
		return nil, e
	}
	var target *domain.Evaluation
	for i := range es {
		if es[i].Revision == rev {
			target = &es[i]
		}
	}
	if target == nil {
		return nil, sql.ErrNoRows
	}
	events, e := s.Store.Audits(id)
	if e != nil {
		return nil, e
	}
	prefix := make([]audit.Event, 0, rev)
	for _, event := range events {
		if event.Revision <= rev {
			prefix = append(prefix, event)
		}
	}
	if report := audit.ValidateDetailed(prefix, rev); !report.Valid {
		return &ReproducibilityResult{FailureCode: "AUDIT_CHAIN_INVALID"}, nil
	}
	if target.AlgorithmVersion != "timesync-eval-v2" {
		return &ReproducibilityResult{FailureCode: "ALGORITHM_UNSUPPORTED"}, nil
	}
	rs, e := s.Store.Rounds(id)
	if e != nil {
		return nil, e
	}
	vs, e := s.Store.RoundVoids(id)
	if e != nil {
		return nil, e
	}
	xs, e := s.Store.SampleExclusions(id)
	if e != nil {
		return nil, e
	}
	rs = effectiveWithoutExclusions(rs, vs, xs)
	probe := *c
	probe.State = domain.Measured
	calc, _, e := probe.BuildEvaluation(rs, time.Unix(0, 0).UTC())
	if e != nil {
		return &ReproducibilityResult{FailureCode: "INPUT_MISSING"}, nil
	}
	out := &ReproducibilityResult{Reproducible: true, InputDigestMatch: calc.InputSummary == target.InputSummary, MetricDifferences: []map[string]any{}}
	if !out.InputDigestMatch {
		out.Reproducible = false
		out.FailureCode = "INPUT_DIGEST_MISMATCH"
	}
	by := map[string]domain.MetricAttribution{}
	for _, m := range target.Metrics {
		by[m.DeviceID+"|"+m.Metric] = m
	}
	for _, m := range calc.Metrics {
		if old, ok := by[m.DeviceID+"|"+m.Metric]; ok && (math.Abs(old.ObservedValue-m.ObservedValue) > 1e-9 || old.Result != m.Result) {
			out.Reproducible = false
			out.MetricDifferences = append(out.MetricDifferences, map[string]any{"device_id": m.DeviceID, "metric": m.Metric, "expected": old.ObservedValue, "actual": m.ObservedValue, "expected_result": old.Result, "actual_result": m.Result})
		}
	}
	seenCalc := map[string]bool{}
	for _, m := range calc.Metrics {
		seenCalc[m.DeviceID+"|"+m.Metric] = true
	}
	for key, old := range by {
		if !seenCalc[key] {
			out.MetricDifferences = append(out.MetricDifferences, map[string]any{"device_id": old.DeviceID, "metric": old.Metric, "failure": "INPUT_MISSING"})
		}
	}
	sort.Slice(out.MetricDifferences, func(i, j int) bool {
		return fmt.Sprint(out.MetricDifferences[i]["device_id"], out.MetricDifferences[i]["metric"]) < fmt.Sprint(out.MetricDifferences[j]["device_id"], out.MetricDifferences[j]["metric"])
	})
	if len(out.MetricDifferences) > 0 {
		out.FailureCode = "METRIC_MISMATCH"
	}
	return out, nil
}

type RemediationEvidenceInput struct {
	DeviationID      string    `json:"deviation_id"`
	PlanVersion      int       `json:"plan_version"`
	EvidenceType     string    `json:"evidence_type"`
	ExecutedBy       string    `json:"executed_by"`
	OccurredAt       time.Time `json:"occurred_at"`
	MaterialSummary  string    `json:"material_summary"`
	ContentDigest    string    `json:"content_digest"`
	SHA256Digest     string    `json:"sha256_digest,omitempty"`
	ExpectedRevision int64     `json:"expected_revision"`
}

func (s *Service) AddRemediationEvidence(id, requestID string, in RemediationEvidenceInput) (*domain.RemediationEvidence, error) {
	c, e := s.get(id)
	if e != nil {
		return nil, e
	}
	if strings.TrimSpace(requestID) == "" {
		return nil, domain.ErrInvalid
	}
	hash, key := requestHash(in), idemKey("remediation-evidence", id, requestID)
	var old domain.RemediationEvidence
	if ok, e := s.replay(key, hash, &old); ok || e != nil {
		return &old, e
	}
	if e = s.check(c, in.ExpectedRevision); e != nil {
		return nil, e
	}
	if c.State != domain.RemediationRequired {
		return nil, domain.ErrState
	}
	if in.ContentDigest == "" {
		in.ContentDigest = in.SHA256Digest
	}
	if in.EvidenceType != "ROOT_CAUSE" && in.EvidenceType != "CONTAINMENT" && in.EvidenceType != "CORRECTIVE_ACTION" || strings.TrimSpace(in.DeviationID) == "" || strings.TrimSpace(in.ExecutedBy) == "" || strings.TrimSpace(in.MaterialSummary) == "" || len(in.ContentDigest) != 64 || !isHexDigest(in.ContentDigest) {
		return nil, domain.ErrInvalid
	}
	ds, e := s.Store.Deviations(id)
	if e != nil {
		return nil, e
	}
	found := false
	for _, d := range ds {
		if d.DeviationID == in.DeviationID && d.Status == "OPEN" {
			found = true
		}
	}
	if !found {
		return nil, sql.ErrNoRows
	}
	plans, e := s.Store.Plans(id)
	if e != nil {
		return nil, e
	}
	validPlan := false
	var plannedAt time.Time
	for _, p := range plans {
		if p.DeviationID == in.DeviationID && p.Version == in.PlanVersion {
			validPlan = true
			plannedAt = p.PlannedAt
		}
	}
	if !validPlan {
		return nil, domain.ErrInvalid
	}
	all, e := s.Store.RemediationEvidence(id)
	if e != nil {
		return nil, e
	}
	for _, x := range all {
		if strings.EqualFold(x.ContentDigest, in.ContentDigest) {
			if x.DeviationID != in.DeviationID || x.PlanVersion != in.PlanVersion || x.EvidenceType != in.EvidenceType || x.MaterialSummary != strings.TrimSpace(in.MaterialSummary) {
				return nil, domain.ErrDuplicate
			}
			var same = x
			return &same, nil
		}
	}
	now := time.Now().UTC()
	if in.OccurredAt.IsZero() {
		in.OccurredAt = now
	}
	if in.OccurredAt.Before(plannedAt) {
		return nil, domain.ErrInvalid
	}
	x := domain.RemediationEvidence{EvidenceID: fmt.Sprintf("re-%s-%d", in.DeviationID, now.UnixNano()), CampaignID: id, DeviationID: in.DeviationID, PlanVersion: in.PlanVersion, EvidenceType: in.EvidenceType, ExecutedBy: in.ExecutedBy, OccurredAt: in.OccurredAt, MaterialSummary: strings.TrimSpace(in.MaterialSummary), ContentDigest: strings.ToLower(in.ContentDigest), CreatedAt: now}
	c.Revision++
	x.Revision = c.Revision
	ev, e := s.newEvent(c, "REMEDIATION_EVIDENCE_ADDED", x.ExecutedBy, requestHash(x))
	if e != nil {
		return nil, e
	}
	e = s.Store.Commit(persistence.Mutation{Campaign: c, RemediationEvidence: []domain.RemediationEvidence{x}, Event: &ev, IdemKey: key, IdemHash: hash, Response: &x})
	return &x, e
}

type ReviewSnapshotResult struct {
	Snapshot *domain.ReviewSnapshot `json:"snapshot"`
	Payload  json.RawMessage        `json:"payload"`
}

func (s *Service) CreateReviewSnapshot(id, reviewer string) (*ReviewSnapshotResult, error) {
	c, e := s.get(id)
	if e != nil {
		return nil, e
	}
	if c.State != domain.ReviewPending {
		return nil, domain.ErrState
	}
	claim, e := s.Store.CurrentReviewClaim(id)
	if e != nil {
		return nil, e
	}
	if claim.ReviewerID != reviewer || claim.Status != "ACTIVE" || !time.Now().UTC().Before(claim.ExpiresAt) {
		return nil, domain.ErrState
	}
	snap, e := s.SnapshotForDevice(id, "all", "")
	if e != nil {
		return nil, e
	}
	payload, _ := json.Marshal(struct {
		Campaign   *domain.Campaign           `json:"campaign"`
		References []domain.ReferenceEvidence `json:"references"`
		Rounds     []domain.MeasurementRound  `json:"rounds"`
		Evaluation *domain.Evaluation         `json:"evaluation"`
		Deviations []domain.DeviationCase     `json:"deviations"`
	}{snap.Campaign, snap.References, snap.Rounds, snap.Evaluation, snap.Deviations})
	sum := sha256.Sum256(payload)
	x := &domain.ReviewSnapshot{CampaignID: id, Revision: c.Revision, ReviewerID: reviewer, ClaimVersion: int64(claim.Version), SnapshotDigest: hex.EncodeToString(sum[:]), ExpiresAt: claim.ExpiresAt, Payload: payload, CreatedAt: time.Now().UTC()}
	if old, e := s.Store.ReviewSnapshot(id, c.Revision); e == nil {
		return &ReviewSnapshotResult{old, old.Payload}, nil
	}
	if e := s.Store.Commit(persistence.Mutation{ReviewSnapshot: x}); e != nil && !strings.Contains(e.Error(), "UNIQUE constraint") {
		return nil, e
	}
	return &ReviewSnapshotResult{x, payload}, nil
}

type ArtifactComparison struct {
	SourceDigest      string           `json:"source_digest"`
	TargetDigest      string           `json:"target_digest"`
	Configuration     map[string]any   `json:"configuration,omitempty"`
	DeviceChanges     []map[string]any `json:"device_changes"`
	MetricDifferences []map[string]any `json:"metric_differences"`
}

func (s *Service) CompareArtifacts(sourceID, targetID string) (*ArtifactComparison, error) {
	if sourceID == targetID {
		return nil, domain.ErrInvalid
	}
	a, e := s.Store.GetArtifact(sourceID)
	if e != nil {
		return nil, e
	}
	b, e := s.Store.GetArtifact(targetID)
	if e != nil {
		return nil, e
	}
	va, vb := audit.VerifyArtifactPayload(a.Payload, ""), audit.VerifyArtifactPayload(b.Payload, "")
	if !va.Valid || !vb.Valid || va.PayloadDigest != a.PayloadDigest || vb.PayloadDigest != b.PayloadDigest {
		return nil, domain.ErrIntegrity
	}
	sc, se := s.get(sourceID)
	tc, te := s.get(targetID)
	if se != nil {
		return nil, se
	}
	if te != nil {
		return nil, te
	}
	if sc.State != domain.Archived || tc.State != domain.Archived {
		return nil, domain.ErrState
	}
	var pa, pb audit.ArtifactPayload
	if json.Unmarshal(a.Payload, &pa) != nil || json.Unmarshal(b.Payload, &pb) != nil {
		return nil, domain.ErrIntegrity
	}
	var ca, cb []domain.Campaign
	if json.Unmarshal(pa.Sections["campaign"], &ca) != nil || json.Unmarshal(pb.Sections["campaign"], &cb) != nil || len(ca) != 1 || len(cb) != 1 {
		return nil, domain.ErrIntegrity
	}
	if ca[0].StationCode != cb[0].StationCode {
		return nil, errors.New("station mismatch")
	}
	var aa, ab []audit.Event
	if json.Unmarshal(pa.Sections["audit"], &aa) != nil || json.Unmarshal(pb.Sections["audit"], &ab) != nil || !audit.ValidateDetailed(aa, ca[0].Revision).Valid || !audit.ValidateDetailed(ab, cb[0].Revision).Valid {
		return nil, domain.ErrIntegrity
	}
	if ca[0].MissionWindowStart.Equal(cb[0].MissionWindowStart) {
		return nil, errors.New("campaigns must be different periods")
	}
	out := &ArtifactComparison{SourceDigest: a.PayloadDigest, TargetDigest: b.PayloadDigest, Configuration: map[string]any{}, DeviceChanges: []map[string]any{}, MetricDifferences: []map[string]any{}}
	if ca[0].Threshold != cb[0].Threshold {
		out.Configuration["threshold_changed"] = true
		out.Configuration["source_threshold"] = ca[0].Threshold
		out.Configuration["target_threshold"] = cb[0].Threshold
	}
	setA, setB := map[string]bool{}, map[string]bool{}
	var ra, rb struct {
		Rounds []domain.MeasurementRound `json:"rounds"`
	}
	json.Unmarshal(pa.Sections["rounds"], &ra)
	json.Unmarshal(pb.Sections["rounds"], &rb)
	for _, r := range ra.Rounds {
		for _, sm := range r.Samples {
			setA[sm.DeviceID] = true
		}
	}
	for _, r := range rb.Rounds {
		for _, sm := range r.Samples {
			setB[sm.DeviceID] = true
		}
	}
	for d := range setA {
		if !setB[d] {
			out.DeviceChanges = append(out.DeviceChanges, map[string]any{"device_id": d, "change": "REMOVED"})
		}
	}
	for d := range setB {
		if !setA[d] {
			out.DeviceChanges = append(out.DeviceChanges, map[string]any{"device_id": d, "change": "ADDED"})
		}
	}
	sort.Slice(out.DeviceChanges, func(i, j int) bool {
		return out.DeviceChanges[i]["device_id"].(string) < out.DeviceChanges[j]["device_id"].(string)
	})
	var ea, eb []domain.Evaluation
	if json.Unmarshal(pa.Sections["evaluations"], &ea) == nil && json.Unmarshal(pb.Sections["evaluations"], &eb) == nil && len(ea) > 0 && len(eb) > 0 {
		ma, mb := map[string]domain.DeviceEvaluation{}, map[string]domain.DeviceEvaluation{}
		for _, d := range ea[len(ea)-1].Devices {
			ma[d.DeviceID] = d
		}
		for _, d := range eb[len(eb)-1].Devices {
			mb[d.DeviceID] = d
		}
		for id, da := range ma {
			if db, ok := mb[id]; ok && (da.MaxAbsDeviation != db.MaxAbsDeviation || da.MeanFrequency != db.MeanFrequency || da.DriftSlope != db.DriftSlope || da.Conclusion != db.Conclusion) {
				out.MetricDifferences = append(out.MetricDifferences, map[string]any{"device_id": id, "max_abs_deviation_delta": db.MaxAbsDeviation - da.MaxAbsDeviation, "mean_frequency_deviation_delta": db.MeanFrequency - da.MeanFrequency, "drift_slope_delta": db.DriftSlope - da.DriftSlope, "from_conclusion": da.Conclusion, "to_conclusion": db.Conclusion})
			}
		}
	}
	sort.Slice(out.MetricDifferences, func(i, j int) bool {
		return out.MetricDifferences[i]["device_id"].(string) < out.MetricDifferences[j]["device_id"].(string)
	})
	return out, nil
}
