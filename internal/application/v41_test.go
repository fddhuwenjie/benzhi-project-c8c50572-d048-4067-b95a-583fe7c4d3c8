package application

import (
	"errors"
	"ground-clock-qualification/internal/domain"
	"testing"
)

func TestCertificateFingerprintReuseAndConflictAreAtomic(t *testing.T) {
	s, start, end := newV40Service(t)
	threshold := domain.ThresholdProfile{MaxAbsDeviation: 1, MaxFrequencyDeviation: 1, MaxDriftSlope: 1}
	c1, err := s.Create(CreateInput{"source", "GS-1", start, end, []string{"D1"}, threshold, "engineer"}, "")
	if err != nil {
		t.Fatal(err)
	}
	base := domain.ReferenceEvidence{EvidenceID: "source-clock", ReferenceKind: "clock", Provider: "National Lab", CertificateDigest: "aabb", SubmittedBy: "engineer", ValidFrom: start.Add(-1), ValidUntil: end.Add(1)}
	if _, err = s.ReferenceDetailed(c1.CampaignID, "source-ref", base, c1.Revision); err != nil {
		t.Fatal(err)
	}
	c2, err := s.Create(CreateInput{"reuse", "GS-2", start, end, []string{"D2"}, threshold, "engineer"}, "")
	if err != nil {
		t.Fatal(err)
	}
	reused := base
	reused.EvidenceID = "reuse-clock"
	result, err := s.ReferenceDetailed(c2.CampaignID, "reuse-ref", reused, c2.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SourceCampaignIDs) != 1 || result.SourceCampaignIDs[0] != "source" {
		t.Fatalf("missing reuse source: %+v", result)
	}
	usage, err := s.ReferenceDigestUsage("reuse", "aabb")
	if err != nil {
		t.Fatal(err)
	}
	if len(usage["usage"].([]domain.DigestUsage)) != 2 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
	c3, err := s.Create(CreateInput{"conflict", "GS-3", start, end, []string{"D3"}, threshold, "engineer"}, "")
	if err != nil {
		t.Fatal(err)
	}
	bad := base
	bad.EvidenceID = "bad"
	bad.ReferenceKind = "frequency"
	_, err = s.ReferenceDetailed(c3.CampaignID, "bad-ref", bad, c3.Revision)
	var conflict *domain.CertificateFingerprintConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("expected fingerprint conflict, got %v", err)
	}
	after, _ := s.Store.GetCampaign(c3.CampaignID)
	events, _ := s.Store.Audits(c3.CampaignID)
	if after.Revision != c3.Revision || len(events) != 1 {
		t.Fatal("conflict changed aggregate or audit")
	}
}
