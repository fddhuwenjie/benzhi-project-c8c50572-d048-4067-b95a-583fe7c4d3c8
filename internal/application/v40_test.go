package application

import (
	"errors"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	"path/filepath"
	"testing"
	"time"
)

func newV40Service(t *testing.T) (*Service, time.Time, time.Time) {
	t.Helper()
	store, err := persistence.Open(filepath.Join(t.TempDir(), "v40.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	return New(store), now.Add(-time.Hour), now.Add(time.Hour)
}

func addReferences(t *testing.T, s *Service, id string, revision int64, start, end time.Time) int64 {
	t.Helper()
	for i, kind := range []string{"clock", "frequency"} {
		result, err := s.ReferenceDetailed(id, "", domain.ReferenceEvidence{EvidenceID: id + "-ref-" + kind, ReferenceKind: kind, Provider: "LAB-" + kind, CertificateDigest: []string{"aa", "bb"}[i], SubmittedBy: "engineer", ValidFrom: start, ValidUntil: end}, revision)
		if err != nil {
			t.Fatal(err)
		}
		revision = result.Campaign.Revision
	}
	return revision
}

func TestAmendmentConflictIsAtomicAndPreflightIsReadOnly(t *testing.T) {
	s, start, end := newV40Service(t)
	c1, err := s.Create(CreateInput{"c1", "GS-1", start, end, []string{"D1"}, domain.ThresholdProfile{MaxAbsDeviation: 1, MaxFrequencyDeviation: 1, MaxDriftSlope: 1}, "engineer"}, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Create(CreateInput{"c2", "GS-2", start, end, []string{"D2"}, domain.ThresholdProfile{MaxAbsDeviation: 1, MaxFrequencyDeviation: 1, MaxDriftSlope: 1}, "engineer"}, "")
	if err != nil {
		t.Fatal(err)
	}
	pre, err := s.ReferencePreflight("c1", []domain.ReferenceEvidence{{EvidenceID: "clock", ReferenceKind: "clock", Provider: "A", CertificateDigest: "cc", ValidFrom: start, ValidUntil: end}, {EvidenceID: "frequency", ReferenceKind: "frequency", Provider: "B", CertificateDigest: "dd", ValidFrom: start, ValidUntil: end}})
	if err != nil || !pre.CanVerify {
		t.Fatalf("unexpected preflight: %+v %v", pre, err)
	}
	afterPre, _ := s.Store.GetCampaign("c1")
	if afterPre.Revision != c1.Revision {
		t.Fatal("preflight changed revision")
	}
	_, err = s.AmendCampaign("c1", "", AmendmentInput{DeviceIDs: []string{"D1", "D2"}, ExpectedRevision: c1.Revision})
	var conflict *domain.ConflictError
	if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 {
		t.Fatalf("expected resource conflict, got %v", err)
	}
	after, _ := s.Store.GetCampaign("c1")
	events, _ := s.Store.Audits("c1")
	if after.Revision != c1.Revision || len(events) != 1 || len(after.DeviceIDs) != 1 {
		t.Fatal("conflict changed aggregate")
	}
	result, err := s.AmendCampaign("c1", "amend-1", AmendmentInput{MissionWindowEnd: end.Add(time.Hour), DeviceIDs: []string{"D1", "D3"}, ExpectedRevision: c1.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if result.Campaign.State != domain.Draft || result.Campaign.Revision != c1.Revision+1 || len(result.Coverage.ClockGaps) != 1 {
		t.Fatalf("unexpected amendment: %+v", result)
	}
}

func TestRoundVoidPreservesRoundAndReplacementRestoresCoverage(t *testing.T) {
	s, start, end := newV40Service(t)
	c, err := s.Create(CreateInput{"void", "GS", start, end, []string{"D1", "D2"}, domain.ThresholdProfile{MaxAbsDeviation: 1, MaxFrequencyDeviation: 1, MaxDriftSlope: 1}, "engineer"}, "")
	if err != nil {
		t.Fatal(err)
	}
	rev := addReferences(t, s, "void", c.Revision, start, end)
	base := start.Add(10 * time.Minute)
	measured, err := s.MeasureBatch("void", []domain.MeasurementRound{{RoundID: "r1", Sequence: 1, OperatorID: "op", Samples: []domain.Sample{{DeviceID: "D1", SampledAt: base}}}, {RoundID: "r2", Sequence: 2, OperatorID: "op", Samples: []domain.Sample{{DeviceID: "D2", SampledAt: base.Add(time.Minute)}}}}, rev)
	if err != nil {
		t.Fatal(err)
	}
	if measured.State != domain.Measured {
		t.Fatal("coverage did not become measured")
	}
	voided, err := s.VoidRound("void", "void-1", RoundVoidInput{"r2", "WIRING_ERROR", "接线错误导致该轮无效", "qa", measured.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if voided.Campaign.State != domain.ReferenceVerified || voided.Void.RoundID != "r2" {
		t.Fatalf("unexpected void: %+v", voided)
	}
	rounds, _ := s.Store.Rounds("void")
	if len(rounds) != 2 || rounds[1].RoundID != "r2" {
		t.Fatal("original round was overwritten")
	}
	restored, err := s.Measure("void", domain.MeasurementRound{RoundID: "r3", Sequence: 3, OperatorID: "op2", ReplacementForRoundID: "r2", Samples: []domain.Sample{{DeviceID: "D2", SampledAt: base.Add(2 * time.Minute)}}}, voided.Campaign.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if restored.State != domain.Measured {
		t.Fatal("replacement did not restore coverage")
	}
	summary, err := s.MeasurementSummary("void", "D2", "original")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Devices[0].SampleCount != 1 || len(summary.RoundIDs) != 2 {
		t.Fatalf("voided round entered summary: %+v", summary)
	}
}

func TestReviewClaimIsConsumedAndArtifactHasSections(t *testing.T) {
	s, start, end := newV40Service(t)
	c, err := s.Create(CreateInput{"claim", "GS", start, end, []string{"D"}, domain.ThresholdProfile{MaxAbsDeviation: 10, MaxFrequencyDeviation: 10, MaxDriftSlope: 10}, "engineer"}, "")
	if err != nil {
		t.Fatal(err)
	}
	rev := addReferences(t, s, "claim", c.Revision, start, end)
	base := start.Add(10 * time.Minute)
	measured, err := s.MeasureBatch("claim", []domain.MeasurementRound{{RoundID: "r1", Sequence: 1, OperatorID: "collector", Samples: []domain.Sample{{DeviceID: "D", TimeOffset: 1, SampledAt: base}}}, {RoundID: "r2", Sequence: 2, OperatorID: "collector", Samples: []domain.Sample{{DeviceID: "D", TimeOffset: 2, SampledAt: base.Add(time.Second)}}}}, rev)
	if err != nil {
		t.Fatal(err)
	}
	evaluated, err := s.EvaluateIdem("claim", measured.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimReview("claim", "", ReviewClaimInput{ReviewerID: "collector", ExpectedRevision: evaluated.Campaign.Revision}); err == nil {
		t.Fatal("collector claimed own work")
	}
	claimed, err := s.ClaimReview("claim", "claim-1", ReviewClaimInput{ReviewerID: "reviewer", DurationMinutes: 30, ExpectedRevision: evaluated.Campaign.Revision})
	if err != nil {
		t.Fatal(err)
	}
	review := domain.Review{ReviewerID: "reviewer", Approved: true, Statement: "同意签发", Checklist: []domain.ReviewItem{{CheckCode: "REFERENCE_TRACEABILITY", Result: "PASS"}, {CheckCode: "MEASUREMENT_COVERAGE", Result: "PASS"}, {CheckCode: "EVALUATION_REPRODUCIBILITY", Result: "PASS"}, {CheckCode: "REMEDIATION_CLOSURE", Result: "PASS"}}}
	approved, err := s.Review("claim", review, claimed.Campaign.Revision)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := s.Archive("claim", approved.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifact.Manifest) != 7 {
		t.Fatalf("missing manifest: %+v", artifact.Manifest)
	}
	if s.VerifySection("claim", "rounds")["valid"] != true {
		t.Fatalf("section verify failed: %+v", s.VerifySection("claim", "rounds"))
	}
	claims, _ := s.Store.ReviewClaims("claim")
	if claims[len(claims)-1].Status != "CONSUMED" {
		t.Fatal("claim was not consumed")
	}
}
