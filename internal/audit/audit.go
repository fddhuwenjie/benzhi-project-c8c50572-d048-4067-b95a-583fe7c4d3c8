package audit

import (
	"encoding/json"
	"fmt"
	"ground-clock-qualification/internal/domain"
	"sort"
	"sync"
	"time"
)

const ArtifactSchemaVersion = "timesync-qualification-v2"
const CanonicalVersion = "canonical-json-v1"

type ArtifactSource struct {
	Campaign            *domain.Campaign
	References          []domain.ReferenceEvidence
	Rounds              []domain.MeasurementRound
	RoundVoids          []domain.RoundVoid
	Evaluations         []domain.Evaluation
	Deviations          []domain.DeviationCase
	Plans               []domain.RemediationPlan
	Reviews             []domain.Review
	Claims              []domain.ReviewClaim
	Events              []Event
	Withdrawals         []domain.ReferenceWithdrawal
	Findings            []domain.ReviewFinding
	Resolutions         []domain.FindingResolution
	Baselines           []domain.DeviceBaseline
	RemediationEvidence []domain.RemediationEvidence
	Exclusions          []domain.SampleExclusion
}
type ArtifactPayload struct {
	SchemaVersion   string                     `json:"schema_version"`
	Sections        map[string]json.RawMessage `json:"sections"`
	Manifest        []domain.SectionManifest   `json:"manifest"`
	AuditHeadDigest string                     `json:"audit_head_digest"`
	PayloadDigest   string                     `json:"payload_digest"`
}
type SectionVerification struct {
	SectionName    string `json:"section_name"`
	Valid          bool   `json:"valid"`
	ExpectedDigest string `json:"expected_digest"`
	ActualDigest   string `json:"actual_digest"`
	RecordCount    int    `json:"record_count"`
}
type ArtifactVerification struct {
	Valid                 bool                  `json:"valid"`
	SchemaVersion         string                `json:"schema_version"`
	PayloadDigest         string                `json:"payload_digest"`
	ExpectedPayloadDigest string                `json:"expected_payload_digest"`
	Sections              []SectionVerification `json:"sections"`
	FailedSections        []string              `json:"failed_sections"`
	Error                 string                `json:"error,omitempty"`
}

type Event struct {
	CampaignID     string    `json:"campaign_id"`
	Revision       int64     `json:"revision"`
	Action         string    `json:"action"`
	Actor          string    `json:"actor"`
	PreviousDigest string    `json:"previous_digest"`
	Digest         string    `json:"digest"`
	At             time.Time `json:"at"`
	Summary        string    `json:"summary,omitempty"`
}
type IntegrityReport struct {
	Valid          bool   `json:"valid"`
	HeadDigest     string `json:"head_digest"`
	ErrorRevision  int64  `json:"error_revision,omitempty"`
	ErrorPosition  int    `json:"error_position,omitempty"`
	ExpectedDigest string `json:"expected_digest,omitempty"`
	ActualDigest   string `json:"actual_digest,omitempty"`
	Error          string `json:"error,omitempty"`
}
type Log struct {
	mu     sync.Mutex
	Events []Event
	Head   string
}

func (l *Log) Append(rev int64, action, actor string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := Event{Revision: rev, Action: action, Actor: actor, PreviousDigest: l.Head, At: time.Unix(0, 0).UTC()}
	b, _ := json.Marshal(e)
	e.Digest = domain.Hash(b)
	l.Events = append(l.Events, e)
	l.Head = e.Digest
}
func NewEvent(campaignID string, rev int64, action, actor, previous string, at time.Time) Event {
	return NewEventWithSummary(campaignID, rev, action, actor, previous, "", at)
}
func NewEventWithSummary(campaignID string, rev int64, action, actor, previous, summary string, at time.Time) Event {
	e := Event{CampaignID: campaignID, Revision: rev, Action: action, Actor: actor, PreviousDigest: previous, At: at.UTC(), Summary: summary}
	b, _ := json.Marshal(e)
	e.Digest = domain.Hash(b)
	return e
}
func Validate(es []Event, revision int64) (string, error) {
	r := ValidateDetailed(es, revision)
	if !r.Valid {
		return r.HeadDigest, fmt.Errorf("%s", r.Error)
	}
	return r.HeadDigest, nil
}
func ValidateDetailed(es []Event, revision int64) IntegrityReport {
	es = NormalizeEvents(es)
	previous := ""
	for i, event := range es {
		expectedRevision := int64(i + 1)
		if event.Revision != expectedRevision {
			return IntegrityReport{ErrorRevision: event.Revision, ErrorPosition: i + 1, ExpectedDigest: previous, ActualDigest: event.PreviousDigest, Error: fmt.Sprintf("audit integrity: expected revision %d", expectedRevision)}
		}
		copy := event
		copy.Digest = ""
		b, _ := json.Marshal(copy)
		expectedDigest := domain.Hash(b)
		if event.PreviousDigest != previous {
			return IntegrityReport{HeadDigest: previous, ErrorRevision: event.Revision, ErrorPosition: i + 1, ExpectedDigest: previous, ActualDigest: event.PreviousDigest, Error: "audit integrity: previous digest mismatch"}
		}
		if event.Digest != expectedDigest {
			return IntegrityReport{HeadDigest: previous, ErrorRevision: event.Revision, ErrorPosition: i + 1, ExpectedDigest: expectedDigest, ActualDigest: event.Digest, Error: "audit integrity: event digest mismatch"}
		}
		previous = event.Digest
	}
	if int64(len(es)) != revision {
		return IntegrityReport{HeadDigest: previous, ErrorRevision: int64(len(es) + 1), ErrorPosition: len(es) + 1, Error: fmt.Sprintf("audit integrity: campaign revision %d, events %d", revision, len(es))}
	}
	return IntegrityReport{Valid: true, HeadDigest: previous}
}
func Artifact(c *domain.Campaign, reviewer string, events []Event, head string) (domain.Artifact, error) {
	payload := struct {
		SchemaVersion string           `json:"schema_version"`
		Campaign      *domain.Campaign `json:"campaign"`
		Events        []Event          `json:"events"`
	}{"1.0", c, NormalizeEvents(events)}
	b, e := json.Marshal(payload)
	if e != nil {
		return domain.Artifact{}, e
	}
	return domain.Artifact{ArtifactID: fmt.Sprintf("artifact-%s", c.CampaignID), CampaignID: c.CampaignID, SchemaVersion: "1.0", PayloadDigest: domain.Hash(b), ReviewerID: reviewer, SignedAt: time.Now().UTC(), AuditHeadDigest: head, Payload: b}, nil
}

func ArtifactWithSections(source ArtifactSource, reviewer, head string) (domain.Artifact, error) {
	if source.Campaign == nil {
		return domain.Artifact{}, domain.ErrInvalid
	}
	// Sort defensive copies so callers' slices keep their original order and
	// the generated artifact remains deterministic. Operating on shared
	// backing arrays would otherwise silently mutate the caller's slices.
	source.References = sortedReferencesCopy(source.References)
	source.Rounds = sortedRoundsCopy(source.Rounds)
	source.RoundVoids = sortedRoundVoidsCopy(source.RoundVoids)
	source.Evaluations = sortedEvaluationsCopy(source.Evaluations)
	source.Deviations = sortedDeviationsCopy(source.Deviations)
	source.Plans = sortedPlansCopy(source.Plans)
	source.Reviews = sortedReviewsCopy(source.Reviews)
	source.Claims = sortedClaimsCopy(source.Claims)
	source.Withdrawals = sortedWithdrawalsCopy(source.Withdrawals)
	source.Findings = sortedFindingsCopy(source.Findings)
	source.Resolutions = sortedResolutionsCopy(source.Resolutions)
	source.Baselines = sortedBaselinesCopy(source.Baselines)
	source.RemediationEvidence = sortedRemediationEvidenceCopy(source.RemediationEvidence)
	source.Exclusions = sortedExclusionsCopy(source.Exclusions)
	source.Events = NormalizeEvents(source.Events)
	type roundsSection struct {
		Rounds     []domain.MeasurementRound    `json:"rounds"`
		Voids      []domain.RoundVoid           `json:"voids"`
		Exclusions []domain.SampleExclusion     `json:"exclusions,omitempty"`
		Plan       domain.MeasurementPlan       `json:"measurement_plan"`
		Compliance domain.MeasurementCompletion `json:"compliance"`
	}
	type referencesSection struct {
		Evidence    []domain.ReferenceEvidence   `json:"evidence"`
		Withdrawals []domain.ReferenceWithdrawal `json:"withdrawals"`
		Coverage    domain.ReferenceCoverage     `json:"coverage"`
		Baselines   []domain.DeviceBaseline      `json:"device_baselines,omitempty"`
	}
	type deviationsSection struct {
		Deviations []domain.DeviationCase       `json:"deviations"`
		Plans      []domain.RemediationPlan     `json:"plans"`
		Evidence   []domain.RemediationEvidence `json:"evidence,omitempty"`
	}
	type reviewsSection struct {
		Reviews     []domain.Review            `json:"reviews"`
		Claims      []domain.ReviewClaim       `json:"claims"`
		Findings    []domain.ReviewFinding     `json:"findings"`
		Resolutions []domain.FindingResolution `json:"resolutions"`
	}
	completion, _ := domain.MeasurementPlanCompliance(source.Campaign, domain.EffectiveRoundsWithExclusions(source.Rounds, source.RoundVoids, source.Exclusions))
	coverage := source.Campaign.ReferenceCoverage(domain.EffectiveReferences(source.References, source.Withdrawals))
	values := map[string]any{
		"campaign":    []domain.Campaign{*source.Campaign},
		"references":  referencesSection{nonNilReferences(source.References), source.Withdrawals, coverage, source.Baselines},
		"rounds":      roundsSection{nonNilRounds(source.Rounds), nonNilVoids(source.RoundVoids), source.Exclusions, source.Campaign.MeasurementPlan, completion},
		"evaluations": nonNilEvaluations(source.Evaluations),
		"deviations":  deviationsSection{nonNilDeviations(source.Deviations), nonNilPlans(source.Plans), source.RemediationEvidence},
		"reviews":     reviewsSection{nonNilReviews(source.Reviews), nonNilClaims(source.Claims), source.Findings, source.Resolutions},
		"audit":       nonNilEvents(source.Events),
	}
	counts := map[string]int{"campaign": 1, "references": len(source.References), "rounds": len(source.Rounds), "evaluations": len(source.Evaluations), "deviations": len(source.Deviations), "reviews": len(source.Reviews), "audit": len(source.Events)}
	order := []string{"campaign", "references", "rounds", "evaluations", "deviations", "reviews", "audit"}
	sections := map[string]json.RawMessage{}
	manifest := make([]domain.SectionManifest, 0, len(order))
	for _, name := range order {
		b, err := json.Marshal(values[name])
		if err != nil {
			return domain.Artifact{}, err
		}
		var normalized any
		if err = json.Unmarshal(b, &normalized); err != nil {
			return domain.Artifact{}, err
		}
		if b, err = json.Marshal(normalized); err != nil {
			return domain.Artifact{}, err
		}
		sections[name] = b
		manifest = append(manifest, domain.SectionManifest{SectionName: name, RecordCount: counts[name], Digest: domain.Hash(b), CanonicalVersion: CanonicalVersion})
	}
	seed := struct {
		SchemaVersion   string                   `json:"schema_version"`
		Manifest        []domain.SectionManifest `json:"manifest"`
		AuditHeadDigest string                   `json:"audit_head_digest"`
	}{ArtifactSchemaVersion, manifest, head}
	seedBytes, err := json.Marshal(seed)
	if err != nil {
		return domain.Artifact{}, err
	}
	digest := domain.Hash(seedBytes)
	payload := ArtifactPayload{ArtifactSchemaVersion, sections, manifest, head, digest}
	body, err := json.Marshal(payload)
	if err != nil {
		return domain.Artifact{}, err
	}
	return domain.Artifact{ArtifactID: fmt.Sprintf("artifact-%s", source.Campaign.CampaignID), CampaignID: source.Campaign.CampaignID, SchemaVersion: ArtifactSchemaVersion, PayloadDigest: digest, ReviewerID: reviewer, AuditHeadDigest: head, SignedAt: time.Now().UTC(), Payload: body, Manifest: manifest}, nil
}

func VerifyArtifactPayload(body []byte, section string) ArtifactVerification {
	out := ArtifactVerification{Valid: false, Sections: []SectionVerification{}, FailedSections: []string{}}
	var payload ArtifactPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		out.Error = "invalid artifact payload"
		return out
	}
	out.SchemaVersion = payload.SchemaVersion
	out.PayloadDigest = payload.PayloadDigest
	if payload.SchemaVersion != ArtifactSchemaVersion {
		out.Error = "schema version mismatch"
	}
	found := section == ""
	for _, item := range payload.Manifest {
		if section != "" && item.SectionName != section {
			continue
		}
		found = true
		raw, ok := payload.Sections[item.SectionName]
		actual := ""
		if ok {
			var normalized any
			if json.Unmarshal(raw, &normalized) == nil {
				if canonical, err := json.Marshal(normalized); err == nil {
					actual = domain.Hash(canonical)
				}
			}
		}
		valid := ok && actual == item.Digest
		out.Sections = append(out.Sections, SectionVerification{item.SectionName, valid, item.Digest, actual, item.RecordCount})
		if !valid {
			out.FailedSections = append(out.FailedSections, item.SectionName)
		}
	}
	if !found {
		out.Error = "unknown section"
		return out
	}
	seed := struct {
		SchemaVersion   string                   `json:"schema_version"`
		Manifest        []domain.SectionManifest `json:"manifest"`
		AuditHeadDigest string                   `json:"audit_head_digest"`
	}{payload.SchemaVersion, payload.Manifest, payload.AuditHeadDigest}
	b, _ := json.Marshal(seed)
	out.ExpectedPayloadDigest = domain.Hash(b)
	if out.ExpectedPayloadDigest != payload.PayloadDigest && out.Error == "" {
		out.Error = "payload digest mismatch"
	}
	out.Valid = out.Error == "" && len(out.FailedSections) == 0
	return out
}

func nonNilReferences(v []domain.ReferenceEvidence) []domain.ReferenceEvidence {
	if v == nil {
		return []domain.ReferenceEvidence{}
	}
	return v
}
func nonNilRounds(v []domain.MeasurementRound) []domain.MeasurementRound {
	if v == nil {
		return []domain.MeasurementRound{}
	}
	return v
}
func nonNilVoids(v []domain.RoundVoid) []domain.RoundVoid {
	if v == nil {
		return []domain.RoundVoid{}
	}
	return v
}
func nonNilEvaluations(v []domain.Evaluation) []domain.Evaluation {
	if v == nil {
		return []domain.Evaluation{}
	}
	return v
}
func nonNilDeviations(v []domain.DeviationCase) []domain.DeviationCase {
	if v == nil {
		return []domain.DeviationCase{}
	}
	return v
}
func nonNilPlans(v []domain.RemediationPlan) []domain.RemediationPlan {
	if v == nil {
		return []domain.RemediationPlan{}
	}
	return v
}
func nonNilReviews(v []domain.Review) []domain.Review {
	if v == nil {
		return []domain.Review{}
	}
	return v
}
func nonNilClaims(v []domain.ReviewClaim) []domain.ReviewClaim {
	if v == nil {
		return []domain.ReviewClaim{}
	}
	return v
}
func nonNilEvents(v []Event) []Event {
	if v == nil {
		return []Event{}
	}
	return v
}

// sortedReferencesCopy returns a sorted copy of the provided reference
// evidence, leaving the caller's slice order untouched.
func sortedReferencesCopy(v []domain.ReferenceEvidence) []domain.ReferenceEvidence {
	out := append([]domain.ReferenceEvidence(nil), v...)
	sort.Slice(out, func(i, j int) bool { return out[i].EvidenceID < out[j].EvidenceID })
	return out
}

func sortedRoundsCopy(v []domain.MeasurementRound) []domain.MeasurementRound {
	out := append([]domain.MeasurementRound(nil), v...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sequence == out[j].Sequence {
			return out[i].RoundID < out[j].RoundID
		}
		return out[i].Sequence < out[j].Sequence
	})
	return out
}

func sortedRoundVoidsCopy(v []domain.RoundVoid) []domain.RoundVoid {
	out := append([]domain.RoundVoid(nil), v...)
	sort.Slice(out, func(i, j int) bool { return out[i].RoundID < out[j].RoundID })
	return out
}

func sortedEvaluationsCopy(v []domain.Evaluation) []domain.Evaluation {
	out := append([]domain.Evaluation(nil), v...)
	sort.Slice(out, func(i, j int) bool { return out[i].Revision < out[j].Revision })
	return out
}

func sortedDeviationsCopy(v []domain.DeviationCase) []domain.DeviationCase {
	out := append([]domain.DeviationCase(nil), v...)
	sort.Slice(out, func(i, j int) bool { return out[i].DeviationID < out[j].DeviationID })
	return out
}

func sortedPlansCopy(v []domain.RemediationPlan) []domain.RemediationPlan {
	out := append([]domain.RemediationPlan(nil), v...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].DeviationID == out[j].DeviationID {
			return out[i].Version < out[j].Version
		}
		return out[i].DeviationID < out[j].DeviationID
	})
	return out
}

func sortedReviewsCopy(v []domain.Review) []domain.Review {
	out := append([]domain.Review(nil), v...)
	sort.Slice(out, func(i, j int) bool { return out[i].Revision < out[j].Revision })
	return out
}

func sortedClaimsCopy(v []domain.ReviewClaim) []domain.ReviewClaim {
	out := append([]domain.ReviewClaim(nil), v...)
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out
}

func sortedWithdrawalsCopy(v []domain.ReferenceWithdrawal) []domain.ReferenceWithdrawal {
	out := append([]domain.ReferenceWithdrawal(nil), v...)
	sort.Slice(out, func(i, j int) bool { return out[i].EvidenceID < out[j].EvidenceID })
	return out
}

func sortedFindingsCopy(v []domain.ReviewFinding) []domain.ReviewFinding {
	out := append([]domain.ReviewFinding(nil), v...)
	sort.Slice(out, func(i, j int) bool { return out[i].FindingID < out[j].FindingID })
	return out
}

func sortedResolutionsCopy(v []domain.FindingResolution) []domain.FindingResolution {
	out := append([]domain.FindingResolution(nil), v...)
	sort.Slice(out, func(i, j int) bool { return out[i].FindingID < out[j].FindingID })
	return out
}

func sortedBaselinesCopy(v []domain.DeviceBaseline) []domain.DeviceBaseline {
	out := append([]domain.DeviceBaseline(nil), v...)
	sort.Slice(out, func(i, j int) bool { return out[i].DeviceID < out[j].DeviceID })
	return out
}

func sortedRemediationEvidenceCopy(v []domain.RemediationEvidence) []domain.RemediationEvidence {
	out := append([]domain.RemediationEvidence(nil), v...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].EvidenceID < out[j].EvidenceID
	})
	return out
}

func sortedExclusionsCopy(v []domain.SampleExclusion) []domain.SampleExclusion {
	out := append([]domain.SampleExclusion(nil), v...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].RoundID == out[j].RoundID {
			return out[i].DeviceID < out[j].DeviceID
		}
		return out[i].RoundID < out[j].RoundID
	})
	return out
}

// Artifact 保留日志实例方法以兼容现有调用方。
func (l *Log) Artifact(c *domain.Campaign, reviewer string) (domain.Artifact, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Artifact(c, reviewer, l.Events, l.Head)
}
func NormalizeEvents(es []Event) []Event {
	out := append([]Event(nil), es...)
	sort.Slice(out, func(i, j int) bool { return out[i].Revision < out[j].Revision })
	return out
}
