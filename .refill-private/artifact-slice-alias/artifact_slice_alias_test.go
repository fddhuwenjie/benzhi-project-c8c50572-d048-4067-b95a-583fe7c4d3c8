package artifact_slice_alias

import (
	"ground-clock-qualification/internal/audit"
	"ground-clock-qualification/internal/domain"
	"testing"
)

func TestArtifactWithSectionsPreservesCallerSliceOrder(t *testing.T) {
	campaign := &domain.Campaign{
		CampaignID:      "campaign-1",
		DeviceIDs:       []string{"device-1"},
		MeasurementPlan: domain.MeasurementPlan{},
	}
	exclusions := []domain.SampleExclusion{
		{CampaignID: campaign.CampaignID, RoundID: "round-2", DeviceID: "device-2"},
		{CampaignID: campaign.CampaignID, RoundID: "round-1", DeviceID: "device-1"},
	}
	withdrawals := []domain.ReferenceWithdrawal{
		{CampaignID: campaign.CampaignID, EvidenceID: "evidence-2"},
		{CampaignID: campaign.CampaignID, EvidenceID: "evidence-1"},
	}
	findings := []domain.ReviewFinding{
		{CampaignID: campaign.CampaignID, FindingID: "finding-2"},
		{CampaignID: campaign.CampaignID, FindingID: "finding-1"},
	}
	resolutions := []domain.FindingResolution{
		{FindingID: "finding-2"},
		{FindingID: "finding-1"},
	}
	baselines := []domain.DeviceBaseline{
		{CampaignID: campaign.CampaignID, DeviceID: "device-2"},
		{CampaignID: campaign.CampaignID, DeviceID: "device-1"},
	}
	remediationEvidence := []domain.RemediationEvidence{
		{CampaignID: campaign.CampaignID, EvidenceID: "evidence-2"},
		{CampaignID: campaign.CampaignID, EvidenceID: "evidence-1"},
	}
	_, err := audit.ArtifactWithSections(audit.ArtifactSource{
		Campaign:            campaign,
		Withdrawals:         withdrawals,
		Findings:            findings,
		Resolutions:         resolutions,
		Baselines:           baselines,
		RemediationEvidence: remediationEvidence,
		Exclusions:          exclusions,
	}, "reviewer-1", "head")
	if err != nil {
		t.Fatalf("构造证据包失败: %v", err)
	}
	if withdrawals[0].EvidenceID != "evidence-2" || withdrawals[1].EvidenceID != "evidence-1" {
		t.Fatalf("证据包构造改写了 withdrawals 顺序: %#v", withdrawals)
	}
	if findings[0].FindingID != "finding-2" || findings[1].FindingID != "finding-1" {
		t.Fatalf("证据包构造改写了 findings 顺序: %#v", findings)
	}
	if resolutions[0].FindingID != "finding-2" || resolutions[1].FindingID != "finding-1" {
		t.Fatalf("证据包构造改写了 resolutions 顺序: %#v", resolutions)
	}
	if baselines[0].DeviceID != "device-2" || baselines[1].DeviceID != "device-1" {
		t.Fatalf("证据包构造改写了 baselines 顺序: %#v", baselines)
	}
	if remediationEvidence[0].EvidenceID != "evidence-2" || remediationEvidence[1].EvidenceID != "evidence-1" {
		t.Fatalf("证据包构造改写了 remediation evidence 顺序: %#v", remediationEvidence)
	}
	if exclusions[0].RoundID != "round-2" || exclusions[1].RoundID != "round-1" {
		t.Fatalf("证据包构造改写了 exclusions 顺序: %#v", exclusions)
	}
}
