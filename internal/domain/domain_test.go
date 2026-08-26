package domain

import (
	"testing"
	"time"
)

func TestCampaignLifecycle(t *testing.T) {
	now := time.Now().UTC()
	c, e := NewCampaign("x", "S", now.Add(-time.Hour), now.Add(time.Hour), []string{"d"}, ThresholdProfile{1, 1, 1}, "op", now)
	if e != nil {
		t.Fatal(e)
	}
	clock := ReferenceEvidence{EvidenceID: "e-clock", ReferenceKind: "clock", Provider: "lab", CertificateDigest: "aa", SubmittedBy: "op", ValidFrom: now.Add(-2 * time.Hour), ValidUntil: now.Add(2 * time.Hour)}
	frequency := ReferenceEvidence{EvidenceID: "e-frequency", ReferenceKind: "frequency", Provider: "lab", CertificateDigest: "bb", SubmittedBy: "op", ValidFrom: now.Add(-2 * time.Hour), ValidUntil: now.Add(2 * time.Hour)}
	if e = c.AddReference(clock, now); e != nil {
		t.Fatal(e)
	}
	if e = c.ValidateReferences([]ReferenceEvidence{clock, frequency}); e != nil {
		t.Fatal(e)
	}
	c.State = ReferenceVerified
	r := MeasurementRound{RoundID: "r", Sequence: 1, OperatorID: "op", Samples: []Sample{{DeviceID: "d", TimeOffset: 2, SampledAt: now}}}
	if e = c.AddMeasurements(r, nil); e != nil {
		t.Fatal(e)
	}
	r2 := MeasurementRound{RoundID: "r2", Sequence: 2, OperatorID: "op", Samples: []Sample{{DeviceID: "d", TimeOffset: 2, SampledAt: now}}}
	ds, e := c.Evaluate([]MeasurementRound{r, r2})
	if e != nil || len(ds) == 0 {
		t.Fatal("expected deviation")
	}
}
