package application

import (
	"database/sql"
	"errors"
	"ground-clock-qualification/internal/audit"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	"sort"
	"strings"
	"time"
)

func (s *Service) ResourceAvailability(station string, start, end time.Time, devices []string) (domain.ResourceAvailability, error) {
	items, err := s.Store.ListCampaigns()
	if err != nil {
		return domain.ResourceAvailability{}, err
	}
	return domain.EvaluateResourceAvailability(station, start, end, devices, items, time.Now().UTC())
}

func (s *Service) certificateSources(candidate domain.ReferenceEvidence) ([]string, error) {
	history, err := s.Store.ReferencesByDigest(candidate.CertificateDigest)
	if err != nil {
		return nil, err
	}
	withdrawals, err := s.Store.AllReferenceWithdrawals()
	if err != nil {
		return nil, err
	}
	withdrawn := map[string]bool{}
	for _, item := range withdrawals {
		withdrawn[item.CampaignID+"|"+item.EvidenceID] = true
	}
	active := history[:0]
	for _, item := range history {
		if !withdrawn[item.CampaignID+"|"+item.EvidenceID] {
			active = append(active, item)
		}
	}
	return domain.CompareCertificateFingerprint(candidate, active)
}

func (s *Service) ReferenceDigestUsage(id, digest string) (map[string]any, error) {
	if _, err := s.get(id); err != nil {
		return nil, err
	}
	digest = strings.TrimSpace(digest)
	if digest == "" {
		return nil, domain.ErrInvalid
	}
	refs, err := s.Store.ReferencesByDigest(digest)
	if err != nil {
		return nil, err
	}
	withdrawals, err := s.Store.AllReferenceWithdrawals()
	if err != nil {
		return nil, err
	}
	withdrawn := map[string]bool{}
	for _, w := range withdrawals {
		withdrawn[w.CampaignID+"|"+w.EvidenceID] = true
	}
	usage := []domain.DigestUsage{}
	for _, r := range refs {
		status := "VALID"
		if withdrawn[r.CampaignID+"|"+r.EvidenceID] {
			status = "WITHDRAWN"
		} else if r.Replaced {
			status = "REPLACED"
		}
		usage = append(usage, domain.DigestUsage{CampaignID: r.CampaignID, EvidenceID: r.EvidenceID, Status: status, SubmittedAt: r.SubmittedAt.UTC(), Fingerprint: domain.CertificateFingerprint(r)})
	}
	sort.Slice(usage, func(i, j int) bool {
		if !usage[i].SubmittedAt.Equal(usage[j].SubmittedAt) {
			return usage[i].SubmittedAt.Before(usage[j].SubmittedAt)
		}
		if usage[i].CampaignID != usage[j].CampaignID {
			return usage[i].CampaignID < usage[j].CampaignID
		}
		return usage[i].EvidenceID < usage[j].EvidenceID
	})
	return map[string]any{"certificate_digest": strings.ToLower(digest), "usage": usage}, nil
}

func (s *Service) MeasurementConsistency(id, deviceID, purpose string) (domain.MeasurementConsistency, error) {
	c, err := s.get(id)
	if err != nil {
		return domain.MeasurementConsistency{}, err
	}
	rounds, err := s.Store.Rounds(id)
	if err != nil {
		return domain.MeasurementConsistency{}, err
	}
	voids, err := s.Store.RoundVoids(id)
	if err != nil {
		return domain.MeasurementConsistency{}, err
	}
	exclusions, err := s.Store.SampleExclusions(id)
	if err != nil {
		return domain.MeasurementConsistency{}, err
	}
	return domain.BuildMeasurementConsistency(c, domain.EffectiveRoundsWithExclusions(rounds, voids, exclusions), deviceID, purpose)
}

func (s *Service) EvaluationMargins(id string, revision int64, deviceID, risk string) (domain.EvaluationMargins, error) {
	if _, err := s.get(id); err != nil {
		return domain.EvaluationMargins{}, err
	}
	evaluations, err := s.Store.Evaluations(id)
	if err != nil {
		return domain.EvaluationMargins{}, err
	}
	var chosen *domain.Evaluation
	for i := range evaluations {
		if revision == 0 || evaluations[i].Revision == revision {
			if chosen == nil || evaluations[i].Revision > chosen.Revision {
				x := evaluations[i]
				chosen = &x
			}
		}
	}
	if chosen == nil {
		return domain.EvaluationMargins{}, errors.New("EVALUATION_NOT_FOUND")
	}
	if deviceID != "" {
		found := false
		for _, d := range chosen.Devices {
			found = found || d.DeviceID == deviceID
		}
		if !found {
			return domain.EvaluationMargins{}, domain.ErrInvalid
		}
	}
	return domain.BuildEvaluationMargins(*chosen, deviceID, risk)
}

type DependencyBatchInput struct {
	Dependencies     []domain.RemediationDependency `json:"dependencies"`
	ExpectedRevision int64                          `json:"expected_revision"`
}
type RemediationBlockedError struct {
	BlockingDeviationIDs []string `json:"blocking_deviation_ids"`
}

func (e *RemediationBlockedError) Error() string { return "REMEDIATION_DEPENDENCY_BLOCKED" }

func (s *Service) AddRemediationDependencies(id, requestID string, in DependencyBatchInput) (*domain.DependencyProjection, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	hash, key := requestHash(in), idemKey("remediation-dependencies", id, requestID)
	var old domain.DependencyProjection
	if ok, e := s.replay(key, hash, &old); ok || e != nil {
		return &old, e
	}
	if strings.TrimSpace(requestID) == "" {
		return nil, domain.ErrInvalid
	}
	if err = s.check(c, in.ExpectedRevision); err != nil {
		return nil, err
	}
	if c.State != domain.RemediationRequired || len(in.Dependencies) == 0 {
		return nil, domain.ErrState
	}
	deviations, err := s.Store.Deviations(id)
	if err != nil {
		return nil, err
	}
	by := map[string]domain.DeviationCase{}
	for _, d := range deviations {
		by[d.DeviationID] = d
	}
	existing, err := s.Store.RemediationDependencies(id)
	if err != nil {
		return nil, err
	}
	version := 1
	for _, e := range existing {
		if e.Version >= version {
			version = e.Version + 1
		}
	}
	latest := map[string]map[string]bool{}
	lastVersion := version - 1
	for _, e := range existing {
		if e.Version == lastVersion {
			if latest[e.DeviationID] == nil {
				latest[e.DeviationID] = map[string]bool{}
			}
			latest[e.DeviationID][e.PrerequisiteDeviationID] = true
		}
	}
	now := time.Now().UTC()
	actor := ""
	batch := make([]domain.RemediationDependency, 0, len(in.Dependencies))
	pairs := map[string]bool{}
	for _, e := range in.Dependencies {
		e.DeviationID = strings.TrimSpace(e.DeviationID)
		e.PrerequisiteDeviationID = strings.TrimSpace(e.PrerequisiteDeviationID)
		e.Reason = strings.TrimSpace(e.Reason)
		e.RegisteredBy = strings.TrimSpace(e.RegisteredBy)
		if actor == "" {
			actor = e.RegisteredBy
		}
		d, ok := by[e.DeviationID]
		p, okp := by[e.PrerequisiteDeviationID]
		if !ok || !okp || d.CampaignID != id || p.CampaignID != id {
			return nil, domain.ErrDeviationScope
		}
		if d.Status != "OPEN" || e.Reason == "" || e.RegisteredBy == "" || e.DeviationID == e.PrerequisiteDeviationID {
			return nil, domain.ErrInvalid
		}
		pair := e.DeviationID + "|" + e.PrerequisiteDeviationID
		if pairs[pair] {
			return nil, domain.ErrInvalid
		}
		pairs[pair] = true
		if len(d.Attempts) > 0 && !latest[e.DeviationID][e.PrerequisiteDeviationID] {
			return nil, domain.ErrState
		}
		e.Version = version
		e.RegisteredAt = now
		e.Revision = c.Revision + 1
		batch = append(batch, e)
	}
	all := append(append([]domain.RemediationDependency(nil), existing...), batch...)
	projection, err := domain.BuildDependencyProjection(deviations, all)
	if err != nil {
		return nil, err
	}
	c.Revision++
	projection.Revision = c.Revision
	event, err := s.newEvent(c, "REMEDIATION_DEPENDENCIES_ADDED", actor, requestHash(batch))
	if err != nil {
		return nil, err
	}
	if err = s.Store.Commit(persistence.Mutation{Campaign: c, RemediationDependencies: batch, Event: &event, IdemKey: key, IdemHash: hash, Response: &projection}); err != nil {
		return nil, err
	}
	return &projection, nil
}

func (s *Service) RemediationDependencies(id string) (*domain.DependencyProjection, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	deviations, _ := s.Store.Deviations(id)
	edges, _ := s.Store.RemediationDependencies(id)
	projection, err := domain.BuildDependencyProjection(deviations, edges)
	if err != nil {
		return nil, err
	}
	projection.Revision = c.Revision
	return &projection, nil
}

func (s *Service) blockingDeviations(id string, deviationIDs []string) ([]string, error) {
	p, err := s.RemediationDependencies(id)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, id := range deviationIDs {
		wanted[id] = true
	}
	out := []string{}
	for _, n := range p.Nodes {
		if wanted[n.DeviationID] && n.Status == "BLOCKED" {
			out = append(out, n.BlockingDeviationIDs...)
		}
	}
	sort.Strings(out)
	return uniqueStrings(out), nil
}
func uniqueStrings(values []string) []string {
	out := []string{}
	for _, v := range values {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	return out
}

func (s *Service) QualificationLineage(id string) (*domain.QualificationLineage, error) {
	all, err := s.Store.ListCampaigns()
	if err != nil {
		return nil, err
	}
	by := map[string]*domain.Campaign{}
	children := map[string][]*domain.Campaign{}
	for _, c := range all {
		by[c.CampaignID] = c
		if c.PredecessorCampaignID != "" {
			children[c.PredecessorCampaignID] = append(children[c.PredecessorCampaignID], c)
		}
	}
	if by[id] == nil {
		return nil, sql.ErrNoRows
	}
	selected := map[string]bool{}
	issues := []domain.LineageIssue{}
	cursor := by[id]
	seen := map[string]bool{}
	for cursor != nil {
		if seen[cursor.CampaignID] {
			issues = append(issues, domain.LineageIssue{Code: "LINEAGE_CYCLE", CampaignID: cursor.CampaignID})
			break
		}
		seen[cursor.CampaignID] = true
		selected[cursor.CampaignID] = true
		if cursor.PredecessorCampaignID == "" {
			break
		}
		next := by[cursor.PredecessorCampaignID]
		if next == nil {
			issues = append(issues, domain.LineageIssue{Code: "LINEAGE_PREDECESSOR_MISSING", CampaignID: cursor.CampaignID, RelatedCampaignID: cursor.PredecessorCampaignID})
			break
		}
		cursor = next
	}
	queue := make([]string, 0, len(selected))
	for campaignID := range selected {
		queue = append(queue, campaignID)
	}
	sort.Strings(queue)
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		cs := children[parent]
		if len(cs) > 1 {
			issues = append(issues, domain.LineageIssue{Code: "LINEAGE_BRANCH", CampaignID: parent})
		}
		for _, child := range cs {
			if !selected[child.CampaignID] {
				selected[child.CampaignID] = true
				queue = append(queue, child.CampaignID)
			}
		}
	}
	nodes := []domain.LineageNode{}
	for cid := range selected {
		c := by[cid]
		node := domain.LineageNode{CampaignID: c.CampaignID, State: c.State, Revision: c.Revision, StationCode: c.StationCode, WindowStart: c.MissionWindowStart, WindowEnd: c.MissionWindowEnd, DeviceIDs: append([]string(nil), c.DeviceIDs...), Threshold: c.Threshold}
		if c.State == domain.Archived {
			valid := true
			artifact, e := s.Store.GetArtifact(cid)
			if e != nil {
				valid = false
				issues = append(issues, domain.LineageIssue{Code: "ARTIFACT_MISSING", CampaignID: cid, Layer: "artifact"})
			} else {
				verification := audit.VerifyArtifactPayload(artifact.Payload, "")
				if !verification.Valid || artifact.PayloadDigest != verification.PayloadDigest || len(verification.Sections) != 7 {
					valid = false
					layer := "payload"
					code := "ARTIFACT_PAYLOAD_INVALID"
					if len(verification.FailedSections) > 0 {
						layer = verification.FailedSections[0]
						code = "ARTIFACT_SECTION_INVALID"
					} else if len(verification.Sections) != 7 {
						layer = "manifest"
						code = "ARTIFACT_SECTION_INVALID"
					}
					issues = append(issues, domain.LineageIssue{Code: code, CampaignID: cid, Layer: layer})
				}
				events, _ := s.Store.Audits(cid)
				report := audit.ValidateDetailed(events, c.Revision)
				if !report.Valid || report.HeadDigest != artifact.AuditHeadDigest {
					valid = false
					issues = append(issues, domain.LineageIssue{Code: "AUDIT_HEAD_INVALID", CampaignID: cid, Layer: "audit"})
				}
			}
			node.ArtifactValid = &valid
		}
		sort.Strings(node.DeviceIDs)
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		if !nodes[i].WindowStart.Equal(nodes[j].WindowStart) {
			return nodes[i].WindowStart.Before(nodes[j].WindowStart)
		}
		return nodes[i].CampaignID < nodes[j].CampaignID
	})
	edges := []domain.LineageEdge{}
	changes := []domain.LineageChange{}
	for _, node := range nodes {
		c := by[node.CampaignID]
		if c.PredecessorCampaignID != "" && selected[c.PredecessorCampaignID] {
			edges = append(edges, domain.LineageEdge{PredecessorCampaignID: c.PredecessorCampaignID, SuccessorCampaignID: c.CampaignID})
			p := by[c.PredecessorCampaignID]
			if p.StationCode != c.StationCode {
				issues = append(issues, domain.LineageIssue{Code: "LINEAGE_STATION_CHANGED", CampaignID: c.CampaignID, RelatedCampaignID: p.CampaignID})
			}
			if c.MissionWindowStart.Before(p.MissionWindowEnd) {
				issues = append(issues, domain.LineageIssue{Code: "LINEAGE_WINDOW_REVERSED", CampaignID: c.CampaignID, RelatedCampaignID: p.CampaignID})
			}
			old, newset := map[string]bool{}, map[string]bool{}
			for _, x := range p.DeviceIDs {
				old[x] = true
			}
			for _, x := range c.DeviceIDs {
				newset[x] = true
			}
			added, removed := []string{}, []string{}
			for x := range newset {
				if !old[x] {
					added = append(added, x)
				}
			}
			for x := range old {
				if !newset[x] {
					removed = append(removed, x)
				}
			}
			sort.Strings(added)
			sort.Strings(removed)
			status := "UNAVAILABLE"
			if c.State == domain.Archived {
				status = "AVAILABLE"
			}
			changes = append(changes, domain.LineageChange{CampaignID: c.CampaignID, AddedDevices: added, RemovedDevices: removed, ThresholdChanged: p.Threshold != c.Threshold, QualificationChecksStatus: status})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].PredecessorCampaignID != edges[j].PredecessorCampaignID {
			return edges[i].PredecessorCampaignID < edges[j].PredecessorCampaignID
		}
		return edges[i].SuccessorCampaignID < edges[j].SuccessorCampaignID
	})
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].CampaignID != issues[j].CampaignID {
			return issues[i].CampaignID < issues[j].CampaignID
		}
		return issues[i].Code < issues[j].Code
	})
	digest := requestHash(struct {
		Nodes   []domain.LineageNode   `json:"nodes"`
		Edges   []domain.LineageEdge   `json:"edges"`
		Changes []domain.LineageChange `json:"changes"`
		Issues  []domain.LineageIssue  `json:"issues"`
	}{nodes, edges, changes, issues})
	return &domain.QualificationLineage{Valid: len(issues) == 0, LineageDigest: digest, Nodes: nodes, Edges: edges, Changes: changes, Issues: issues}, nil
}
