package application

import (
	"ground-clock-qualification/internal/domain"
	"testing"
	"time"
)

func testThreshold() domain.ThresholdProfile {
	return domain.ThresholdProfile{MaxAbsDeviation: 1, MaxFrequencyDeviation: 1, MaxDriftSlope: 1}
}

func TestCancellationReleasesResourcesAndWithdrawalFallsBack(t *testing.T) {
	s, start, end := newV40Service(t)
	c, err := s.Create(CreateInput{"cancel-me", "GS-C", start, end, []string{"D1"}, testThreshold(), "creator"}, "create-cancel")
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := s.CancelCampaign(c.CampaignID, "cancel-key", CancelInput{CancelledBy: "scheduler", ReasonCode: "MISSION_CANCELLED", Reason: "任务计划已经取消", RequestID: "cancel-request", ExpectedRevision: c.Revision})
	if err != nil || cancelled.State != domain.Cancelled {
		t.Fatalf("cancel failed: %+v %v", cancelled, err)
	}
	if _, err = s.Create(CreateInput{"replacement", "GS-C", start, end, []string{"D1"}, testThreshold(), "creator"}, "replacement-key"); err != nil {
		t.Fatalf("cancelled resources were not released: %v", err)
	}

	w, err := s.Create(CreateInput{"withdraw", "GS-W", start, end, []string{"D2"}, testThreshold(), "creator"}, "create-withdraw")
	if err != nil {
		t.Fatal(err)
	}
	rev := addReferences(t, s, w.CampaignID, w.Revision, start, end)
	result, err := s.WithdrawReference(w.CampaignID, "withdraw-key", WithdrawalInput{EvidenceID: w.CampaignID + "-ref-clock", ReasonCode: "CERTIFICATE_REVOKED", Reason: "证书来源已经失效", WithdrawnBy: "quality", ExpectedRevision: rev})
	if err != nil || result.Campaign.State != domain.Draft || len(result.Coverage.ClockGaps) != 1 {
		t.Fatalf("withdrawal did not recalculate coverage: %+v %v", result, err)
	}
	refs, _ := s.Store.References(w.CampaignID)
	withdrawals, _ := s.Store.ReferenceWithdrawals(w.CampaignID)
	if len(refs) != 2 || len(withdrawals) != 1 {
		t.Fatalf("withdrawal mutated evidence: refs=%d withdrawals=%d", len(refs), len(withdrawals))
	}
	replacement := domain.ReferenceEvidence{EvidenceID: "clock-new", ReferenceKind: "clock", Provider: "lab-new", CertificateDigest: "cc", SubmittedBy: "quality", ValidFrom: start, ValidUntil: end}
	verified, err := s.ReferenceDetailed(w.CampaignID, "new-clock", replacement, result.Campaign.Revision)
	if err != nil || verified.Campaign.State != domain.ReferenceVerified {
		t.Fatalf("replacement evidence did not restore coverage: %+v %v", verified, err)
	}
}

func TestMeasurementPlanAndSimulationAreConsistentAndReadOnly(t *testing.T) {
	s, start, end := newV40Service(t)
	plan := domain.MeasurementPlan{RequiredRounds: 3, MinSpanSeconds: 60, MaxIntervalSeconds: 40, MinTemperature: 18, MaxTemperature: 26, MinHumidity: 20, MaxHumidity: 80, AllowedSignalStatus: []string{"GOOD"}}
	c, err := s.CreateWithPlan(CreateInput{"planned", "GS-P", start, end, []string{"D1"}, testThreshold(), "creator"}, &plan, "create-plan")
	if err != nil {
		t.Fatal(err)
	}
	rev := addReferences(t, s, c.CampaignID, c.Revision, start, end)
	base := start.Add(10 * time.Minute)
	makeRound := func(id string, seq int, at time.Time, temp string, offset float64) domain.MeasurementRound {
		return domain.MeasurementRound{RoundID: id, Sequence: seq, OperatorID: "operator", Environment: map[string]string{"temperature": temp, "humidity": "40", "signal_status": "GOOD"}, Samples: []domain.Sample{{DeviceID: "D1", TimeOffset: offset, SampledAt: at}}}
	}
	first, err := s.MeasureBatch(c.CampaignID, []domain.MeasurementRound{makeRound("r1", 1, base, "20", .1), makeRound("r2", 2, base.Add(30*time.Second), "20", .1)}, rev)
	if err != nil || first.State != domain.ReferenceVerified {
		t.Fatalf("plan completed too early: %+v %v", first, err)
	}
	bad := makeRound("r3-bad", 3, base.Add(60*time.Second), "30", .2)
	preflight, err := s.MeasurePreflight(c.CampaignID, []domain.MeasurementRound{bad})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range preflight.Issues {
		if issue.Code == "TEMPERATURE_OUT_OF_PLAN" && issue.RoundID == bad.RoundID && issue.DeviceID == "D1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("preflight lacked located plan issue: %+v", preflight.Issues)
	}
	before := first.Revision
	if _, err = s.MeasureBatch(c.CampaignID, []domain.MeasurementRound{bad}, before); err == nil {
		t.Fatal("formal measure accepted plan violation")
	}
	saved, _ := s.Store.GetCampaign(c.CampaignID)
	rounds, _ := s.Store.Rounds(c.CampaignID)
	if saved.Revision != before || len(rounds) != 2 {
		t.Fatal("failed plan batch was not atomic")
	}
	measured, err := s.MeasureBatch(c.CampaignID, []domain.MeasurementRound{makeRound("r3", 3, base.Add(60*time.Second), "20", .2)}, before)
	if err != nil || measured.State != domain.Measured {
		t.Fatalf("compliant third round failed: %+v %v", measured, err)
	}
	sim, err := s.SimulateEvaluation(c.CampaignID, domain.ThresholdProfile{MaxAbsDeviation: .15, MaxFrequencyDeviation: 1, MaxDriftSlope: 1}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sim.Devices) != 1 || sim.Devices[0].Change != "PASS_TO_FAIL" {
		t.Fatalf("unexpected simulation: %+v", sim)
	}
	after, _ := s.Store.GetCampaign(c.CampaignID)
	evaluations, _ := s.Store.Evaluations(c.CampaignID)
	if after.Revision != measured.Revision || len(evaluations) != 0 {
		t.Fatal("simulation changed formal state")
	}
}

func TestReviewFindingClosureAndArchivedSuccessorQualification(t *testing.T) {
	s, start, end := newV40Service(t)
	c, err := s.Create(CreateInput{"reviewed", "GS-R", start, end, []string{"D1"}, testThreshold(), "creator"}, "create-review")
	if err != nil {
		t.Fatal(err)
	}
	rev := addReferences(t, s, c.CampaignID, c.Revision, start, end)
	base := start.Add(10 * time.Minute)
	measured, err := s.MeasureBatch(c.CampaignID, []domain.MeasurementRound{{RoundID: "r1", Sequence: 1, OperatorID: "operator", Samples: []domain.Sample{{DeviceID: "D1", TimeOffset: .1, SampledAt: base}}}, {RoundID: "r2", Sequence: 2, OperatorID: "operator", Samples: []domain.Sample{{DeviceID: "D1", TimeOffset: .1, SampledAt: base.Add(time.Second)}}}}, rev)
	if err != nil {
		t.Fatal(err)
	}
	evaluated, err := s.EvaluateIdem(c.CampaignID, measured.Revision, "eval")
	if err != nil {
		t.Fatal(err)
	}
	checklist := []domain.ReviewItem{{CheckCode: "REFERENCE_TRACEABILITY", Result: "FAIL", Note: "参考说明材料缺失", Severity: "MINOR"}, {CheckCode: "MEASUREMENT_COVERAGE", Result: "FAIL", Note: "设备复测材料不足", DeviceID: "D1", Severity: "MAJOR", RequiresRetest: true}, {CheckCode: "EVALUATION_REPRODUCIBILITY", Result: "PASS"}, {CheckCode: "REMEDIATION_CLOSURE", Result: "PASS"}}
	rejected, err := s.ReviewIdem(c.CampaignID, domain.Review{ReviewerID: "reviewer", Reason: "复核发现两项问题", Checklist: checklist}, evaluated.Campaign.Revision, "reject")
	if err != nil || rejected.State != domain.RemediationRequired {
		t.Fatalf("review rejection failed: %+v %v", rejected, err)
	}
	findings, _ := s.Store.ReviewFindings(c.CampaignID)
	if len(findings) != 2 {
		t.Fatalf("expected two findings, got %d", len(findings))
	}
	var document, technical domain.ReviewFinding
	for _, f := range findings {
		if f.RequiresRetest {
			technical = f
		} else {
			document = f
		}
	}
	partial, err := s.ResolveReviewFindings(c.CampaignID, "resolve-doc", ResolutionRequest{ExpectedRevision: rejected.Revision, Resolutions: []ResolutionInput{{FindingID: document.FindingID, Resolution: "补齐参考说明", ResolvedBy: "engineer", EvidenceSummary: "已补齐参考说明摘要"}}})
	if err != nil || partial.Campaign.State != domain.RemediationRequired {
		t.Fatalf("document resolution failed: %+v %v", partial, err)
	}
	retest := domain.MeasurementRound{RoundID: "review-retest", Sequence: 3, OperatorID: "engineer", Samples: []domain.Sample{{DeviceID: "D1", TimeOffset: .1, SampledAt: base.Add(2 * time.Second)}}}
	captured, err := s.RetestAttempt(c.CampaignID, "capture-retest", partial.Campaign.Revision, nil, retest)
	if err != nil {
		t.Fatalf("review retest failed: %v", err)
	}
	closed, err := s.ResolveReviewFindings(c.CampaignID, "resolve-tech", ResolutionRequest{ExpectedRevision: captured.Campaign.Revision, Resolutions: []ResolutionInput{{FindingID: technical.FindingID, Resolution: "补充合格复测", ResolvedBy: "engineer", EvidenceSummary: "新复测数据满足门限", RetestRoundID: retest.RoundID}}})
	if err != nil || closed.Campaign.State != domain.ReviewPending {
		t.Fatalf("technical resolution failed: %+v %v", closed, err)
	}
	pass := []domain.ReviewItem{{CheckCode: "REFERENCE_TRACEABILITY", Result: "PASS"}, {CheckCode: "MEASUREMENT_COVERAGE", Result: "PASS"}, {CheckCode: "EVALUATION_REPRODUCIBILITY", Result: "PASS"}, {CheckCode: "REMEDIATION_CLOSURE", Result: "PASS"}}
	ids := []string{findings[0].FindingID, findings[1].FindingID}
	approved, err := s.ReviewIdem(c.CampaignID, domain.Review{ReviewerID: "reviewer-2", Approved: true, Statement: "同意再次提交", Checklist: pass, FindingIDs: ids}, closed.Campaign.Revision, "approve-again")
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := s.Archive(c.CampaignID, approved.Revision)
	if err != nil {
		t.Fatal(err)
	}
	check, err := s.QualificationCheck(c.CampaignID, "GS-R", start.Add(time.Minute), end.Add(-time.Minute), []string{"D1"})
	if err != nil || !check.OverallQualified || check.EvidenceDigest != artifact.PayloadDigest {
		t.Fatalf("qualification check failed: %+v %v", check, err)
	}
	successor, err := s.CreateSuccessor(c.CampaignID, "successor-key", SuccessorInput{CampaignID: "reviewed-next", MissionWindowStart: end.Add(time.Hour), MissionWindowEnd: end.Add(2 * time.Hour), CreatedBy: "next-owner"})
	if err != nil {
		t.Fatal(err)
	}
	if successor.State != domain.Draft || successor.Revision != 1 || successor.PredecessorCampaignID != c.CampaignID {
		t.Fatalf("unexpected successor: %+v", successor)
	}
	snap, err := s.Snapshot(successor.CampaignID, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.References) != 0 || len(snap.Rounds) != 0 || len(snap.Reviews) != 0 || snap.Artifact != nil {
		t.Fatal("successor copied evidence chain")
	}
	if _, err = s.CreateSuccessor(c.CampaignID, "successor-key", SuccessorInput{CampaignID: "different", MissionWindowStart: end.Add(3 * time.Hour), MissionWindowEnd: end.Add(4 * time.Hour), CreatedBy: "next-owner"}); err == nil || err.Error() != "idempotency key conflict" {
		t.Fatalf("successor idempotency conflict missing: %v", err)
	}
}
