package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"ground-clock-qualification/internal/application"
	"ground-clock-qualification/internal/domain"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Server struct{ App *application.Service }

func New(a *application.Service) *Server { return &Server{App: a} }
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/readyz", s.ready)
	m.HandleFunc("/api/v1/campaigns/resource-availability", s.resourceAvailability)
	m.HandleFunc("/api/v1/campaigns", s.create)
	m.HandleFunc("/api/v1/campaigns/remediation-queue", s.remediationQueue)
	m.HandleFunc("/api/v1/campaigns/", s.campaign)
	return m
}

func (s *Server) resourceAvailability(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var q struct {
		StationCode   string `json:"station_code"`
		MissionWindow struct {
			Start time.Time `json:"start"`
			End   time.Time `json:"end"`
		} `json:"mission_window"`
		DeviceIDs []string `json:"device_ids"`
	}
	if read(w, r, &q) != nil {
		write(w, map[string]string{"error": "invalid_json"}, 400)
		return
	}
	result, err := s.App.ResourceAvailability(q.StationCode, q.MissionWindow.Start, q.MissionWindow.End, q.DeviceIDs)
	respond(w, result, err)
}
func write(w http.ResponseWriter, v any, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func read(w http.ResponseWriter, r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("body")
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing json")
	}
	return nil
}
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	write(w, map[string]string{"status": "ok"}, 200)
}

type createReq struct {
	CampaignID      string                  `json:"campaign_id"`
	StationCode     string                  `json:"station_code"`
	Start           string                  `json:"start"`
	End             string                  `json:"end"`
	CreatedBy       string                  `json:"created_by"`
	DeviceIDs       []string                `json:"device_ids"`
	Threshold       domain.ThresholdProfile `json:"threshold"`
	MeasurementPlan *domain.MeasurementPlan `json:"measurement_plan,omitempty"`
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		q := r.URL.Query()
		if st := q.Get("state"); st != "" {
			valid := map[string]bool{"DRAFT": true, "REFERENCE_VERIFIED": true, "MEASURED": true, "REMEDIATION_REQUIRED": true, "REVIEW_PENDING": true, "REVIEW_APPROVED": true, "ARCHIVED": true, "CANCELLED": true}
			if !valid[st] {
				write(w, map[string]string{"error": "invalid_state"}, 400)
				return
			}
		}
		off, err := queryInt(q.Get("offset"), 0)
		if err != nil || off < 0 {
			write(w, map[string]string{"error": "invalid_pagination"}, 400)
			return
		}
		lim, err := queryInt(q.Get("limit"), 50)
		if err != nil || lim < 0 || lim > 100 {
			write(w, map[string]string{"error": "invalid_limit"}, 400)
			return
		}
		if lim == 0 {
			lim = 50
		}
		startRaw := first(q.Get("window_start"), q.Get("mission_window_start"), q.Get("start"))
		endRaw := first(q.Get("window_end"), q.Get("mission_window_end"), q.Get("end"))
		start, errStart := optionalTime(startRaw)
		end, errEnd := optionalTime(endRaw)
		if errStart != nil || errEnd != nil || (start != nil && end != nil && end.Before(*start)) {
			write(w, map[string]string{"error": "invalid_time_range"}, 400)
			return
		}
		result, e := s.App.ListCampaigns(application.ListFilter{State: q.Get("state"), Station: q.Get("station_code"), ReviewerID: q.Get("reviewer_id"), CancellationReason: first(q.Get("cancellation_reason"), q.Get("cancellation_reason_code")), WindowStart: start, WindowEnd: end, Offset: off, Limit: lim})
		if e != nil {
			write(w, map[string]string{"error": e.Error()}, 500)
			return
		}
		write(w, result, 200)
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var q createReq
	if read(w, r, &q) != nil {
		write(w, map[string]string{"error": "invalid_json"}, 400)
		return
	}
	st, e1 := time.Parse(time.RFC3339, q.Start)
	en, e2 := time.Parse(time.RFC3339, q.End)
	if e1 != nil || e2 != nil {
		write(w, map[string]string{"error": "invalid_time"}, 400)
		return
	}
	c, e := s.App.CreateWithPlan(application.CreateInput{CampaignID: q.CampaignID, StationCode: q.StationCode, Start: st, End: en, Devices: q.DeviceIDs, Threshold: q.Threshold, By: q.CreatedBy}, q.MeasurementPlan, r.Header.Get("Idempotency-Key"))
	if e != nil {
		respond(w, nil, e)
		return
	}
	write(w, c, 201)
}
func queryInt(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	return strconv.Atoi(raw)
}
func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
func optionalTime(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	value = value.UTC()
	return &value, nil
}
func (s *Server) campaign(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		w.WriteHeader(404)
		return
	}
	id := parts[3]
	if len(parts) == 4 && r.Method == "GET" {
		include := r.URL.Query().Get("include")
		if include == "" {
			include = "all"
		}
		if st := r.URL.Query().Get("state"); st != "" {
			if st != "DRAFT" && st != "REFERENCE_VERIFIED" && st != "MEASURED" && st != "REMEDIATION_REQUIRED" && st != "REVIEW_PENDING" && st != "REVIEW_APPROVED" && st != "ARCHIVED" && st != "CANCELLED" {
				write(w, map[string]string{"error": "invalid_state"}, 400)
				return
			}
		}
		snap, e := s.App.SnapshotForDevice(id, include, r.URL.Query().Get("device_id"))
		if e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				write(w, map[string]string{"error": "not_found"}, 404)
			} else {
				write(w, map[string]string{"error": e.Error()}, 422)
			}
		} else {
			write(w, snap, 200)
		}
		return
	}
	action := ""
	if len(parts) > 4 {
		action = parts[4]
	}
	if action == "reference-digest-usage" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		result, e := s.App.ReferenceDigestUsage(id, r.URL.Query().Get("certificate_digest"))
		respond(w, result, e)
		return
	}
	if action == "measurement-consistency" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		result, e := s.App.MeasurementConsistency(id, r.URL.Query().Get("device_id"), r.URL.Query().Get("purpose"))
		respond(w, result, e)
		return
	}
	if action == "environment-correlation" {
		if len(parts) != 5 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		allowed := map[string]bool{"device_id": true, "environment_field": true, "deviation_metric": true}
		for key := range q {
			if !allowed[key] {
				write(w, map[string]string{"error": "unknown_parameter", "parameter": key}, http.StatusBadRequest)
				return
			}
		}
		result, e := s.App.EnvironmentCorrelation(id, q.Get("device_id"), q.Get("environment_field"), q.Get("deviation_metric"))
		respond(w, result, e)
		return
	}
	if action == "evaluation-margins" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		revision, e := queryInt64(r.URL.Query().Get("revision"), 0)
		if e != nil || revision < 0 {
			write(w, map[string]string{"error": "invalid_revision"}, 400)
			return
		}
		result, err := s.App.EvaluationMargins(id, revision, r.URL.Query().Get("device_id"), r.URL.Query().Get("risk_level"))
		respond(w, result, err)
		return
	}
	if action == "qualification-lineage" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		result, e := s.App.QualificationLineage(id)
		respond(w, result, e)
		return
	}
	if action == "remediation-dependencies" {
		if r.Method == http.MethodGet {
			result, e := s.App.RemediationDependencies(id)
			respond(w, result, e)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var q application.DependencyBatchInput
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if raw := strings.Trim(r.Header.Get("If-Match"), `"`); raw != "" {
			revision, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || revision < 1 {
				write(w, map[string]string{"error": "invalid_revision"}, 400)
				return
			}
			q.ExpectedRevision = revision
		}
		result, e := s.App.AddRemediationDependencies(id, r.Header.Get("Idempotency-Key"), q)
		respond(w, result, e)
		return
	}
	if action == "reference-resilience" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		result, e := s.App.ReferenceResilience(id, r.URL.Query().Get("reference_kind"))
		respond(w, result, e)
		return
	}
	if action == "remediation-effectiveness" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		result, e := s.App.RemediationEffectiveness(id, r.URL.Query().Get("device_id"), r.URL.Query().Get("metric"), r.URL.Query().Get("status"))
		respond(w, result, e)
		return
	}
	if action == "reviewer-eligibility" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var q struct {
			ReviewerIDs []string `json:"reviewer_ids"`
		}
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		result, e := s.App.ReviewerEligibility(id, q.ReviewerIDs)
		respond(w, result, e)
		return
	}
	if action == "artifact" || action == "verify" || action == "audit" || action == "evaluations" || action == "remediation-plans" || action == "archive-preflight" || action == "measurement-summary" || action == "review-findings" || action == "device-baselines" || action == "reference-batches" || action == "sample-exclusions" || action == "remediation-evidence" || action == "review-snapshots" || action == "artifact-comparison" {
		if action == "remediation-plans" && r.Method == "POST" {
		} else if action == "review-findings" && r.Method == "POST" && len(parts) > 5 && parts[5] == "resolutions" {
		} else if action == "device-baselines" || action == "reference-batches" || action == "sample-exclusions" || action == "remediation-evidence" || action == "review-snapshots" {
			if r.Method != "POST" && !(action == "remediation-evidence" && r.Method == "GET") {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
		} else if action == "artifact-comparison" {
			if r.Method != "GET" {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
		} else if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
	} else if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	revRaw := first(r.Header.Get("If-Match"), r.URL.Query().Get("expected_revision"))
	var rev int64
	if revRaw != "" {
		var err error
		rev, err = strconv.ParseInt(strings.Trim(revRaw, `"`), 10, 64)
		if err != nil || rev < 0 {
			write(w, map[string]string{"error": "invalid_revision"}, 400)
			return
		}
	}
	switch action {
	case "device-baselines":
		var q application.BaselineRegistration
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		q.ExpectedRevision = rev
		result, e := s.App.RegisterDeviceBaselines(id, r.Header.Get("Idempotency-Key"), q)
		respond(w, result, e)
	case "reference-batches":
		var q application.ReferenceBatch
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		q.ExpectedRevision = rev
		result, e := s.App.ConfirmReferenceBatch(id, r.Header.Get("Idempotency-Key"), q)
		respond(w, result, e)
	case "sample-exclusions":
		var q application.SampleExclusionInput
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		q.ExpectedRevision = rev
		result, e := s.App.ExcludeSample(id, r.Header.Get("Idempotency-Key"), q)
		respond(w, result, e)
	case "remediation-evidence":
		if r.Method == "GET" {
			result, e := s.App.Store.RemediationEvidence(id)
			respond(w, result, e)
			return
		}
		var q application.RemediationEvidenceInput
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		q.ExpectedRevision = rev
		result, e := s.App.AddRemediationEvidence(id, r.Header.Get("Idempotency-Key"), q)
		respond(w, result, e)
	case "review-snapshots":
		var q struct {
			ReviewerID string `json:"reviewer_id"`
		}
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		result, e := s.App.CreateReviewSnapshot(id, q.ReviewerID)
		respond(w, result, e)
	case "artifact-comparison":
		against := r.URL.Query().Get("against_campaign_id")
		if against == "" {
			write(w, map[string]string{"error": "against_campaign_id_required"}, 400)
			return
		}
		result, e := s.App.CompareArtifacts(id, against)
		respond(w, result, e)
	case "successors":
		var q application.SuccessorInput
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		result, e := s.App.CreateSuccessor(id, r.Header.Get("Idempotency-Key"), q)
		respond(w, result, e)
	case "cancel":
		var q application.CancelInput
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		q.ExpectedRevision = rev
		result, e := s.App.CancelCampaign(id, r.Header.Get("Idempotency-Key"), q)
		respond(w, result, e)
	case "reference-withdrawals":
		var q application.WithdrawalInput
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		q.ExpectedRevision = rev
		result, e := s.App.WithdrawReference(id, r.Header.Get("Idempotency-Key"), q)
		respond(w, result, e)
	case "evaluation-simulations":
		var q struct {
			ThresholdProfile domain.ThresholdProfile `json:"threshold_profile"`
			AlgorithmVersion string                  `json:"algorithm_version,omitempty"`
		}
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		result, e := s.App.SimulateEvaluation(id, q.ThresholdProfile, q.AlgorithmVersion)
		respond(w, result, e)
	case "review-findings":
		if r.Method == "GET" {
			if len(parts) > 5 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			findings, e := s.App.Store.ReviewFindings(id)
			if e != nil {
				respond(w, nil, e)
				return
			}
			resolutions, e := s.App.Store.FindingResolutions(id)
			respond(w, map[string]any{"findings": findings, "resolutions": resolutions}, e)
			return
		}
		if len(parts) <= 5 || parts[5] != "resolutions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var q application.ResolutionRequest
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		q.ExpectedRevision = rev
		result, e := s.App.ResolveReviewFindings(id, r.Header.Get("Idempotency-Key"), q)
		respond(w, result, e)
	case "qualification-checks":
		batch, batched, err := decodeQualificationBatch(w, r)
		if err != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if !batched && len(batch.Checks) == 1 {
			q := batch.Checks[0]
			result, e := s.App.QualificationCheck(id, q.StationCode, q.WindowStart, q.WindowEnd, q.DeviceIDs)
			respond(w, result, e)
			return
		}
		result, e := s.App.QualificationBatch(id, batch)
		respond(w, result, e)
	case "amendments":
		var q struct {
			StationCode        string    `json:"station_code"`
			MissionWindowStart time.Time `json:"mission_window_start"`
			MissionWindowEnd   time.Time `json:"mission_window_end"`
			MissionWindow      struct {
				Start time.Time `json:"start"`
				End   time.Time `json:"end"`
			} `json:"mission_window"`
			DeviceIDs        []string                `json:"device_ids"`
			ThresholdProfile domain.ThresholdProfile `json:"threshold_profile"`
			Threshold        domain.ThresholdProfile `json:"threshold"`
			ExpectedRevision int64                   `json:"expected_revision"`
			MeasurementPlan  *domain.MeasurementPlan `json:"measurement_plan,omitempty"`
		}
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if q.MissionWindowStart.IsZero() {
			q.MissionWindowStart = q.MissionWindow.Start
		}
		if q.MissionWindowEnd.IsZero() {
			q.MissionWindowEnd = q.MissionWindow.End
		}
		if q.ThresholdProfile == (domain.ThresholdProfile{}) {
			q.ThresholdProfile = q.Threshold
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		result, e := s.App.AmendCampaign(id, r.Header.Get("Idempotency-Key"), application.AmendmentInput{StationCode: q.StationCode, MissionWindowStart: q.MissionWindowStart, MissionWindowEnd: q.MissionWindowEnd, DeviceIDs: q.DeviceIDs, ThresholdProfile: q.ThresholdProfile, ExpectedRevision: rev, MeasurementPlan: q.MeasurementPlan})
		respond(w, result, e)
	case "reference-preflight":
		var raw json.RawMessage
		if read(w, r, &raw) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		var candidates []domain.ReferenceEvidence
		if len(raw) > 0 && raw[0] == '[' {
			if strictUnmarshal(raw, &candidates) != nil {
				write(w, map[string]string{"error": "invalid_json"}, 400)
				return
			}
		} else {
			var q struct {
				Candidates []domain.ReferenceEvidence `json:"candidates"`
			}
			if strictUnmarshal(raw, &q) != nil {
				write(w, map[string]string{"error": "invalid_json"}, 400)
				return
			}
			candidates = q.Candidates
		}
		if len(candidates) == 0 {
			write(w, map[string]string{"error": "candidates_required"}, 400)
			return
		}
		result, e := s.App.ReferencePreflight(id, candidates)
		respond(w, result, e)
	case "round-voids":
		var q application.RoundVoidInput
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		q.ExpectedRevision = rev
		result, e := s.App.VoidRound(id, r.Header.Get("Idempotency-Key"), q)
		respond(w, result, e)
	case "measurement-summary":
		result, e := s.App.MeasurementSummary(id, r.URL.Query().Get("device_id"), r.URL.Query().Get("purpose"))
		respond(w, result, e)
	case "remediation-preflight":
		var q struct {
			DeviationIDs    []string                 `json:"deviation_ids"`
			CandidateRetest *domain.MeasurementRound `json:"candidate_retest"`
		}
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		result, e := s.App.RemediationPreflight(id, q.DeviationIDs, q.CandidateRetest)
		respond(w, result, e)
	case "review-claims":
		var q application.ReviewClaimInput
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		q.ExpectedRevision = rev
		if len(parts) > 5 && parts[5] == "release" {
			result, e := s.App.ReleaseReviewClaim(id, r.Header.Get("Idempotency-Key"), q)
			respond(w, result, e)
		} else {
			result, e := s.App.ClaimReview(id, r.Header.Get("Idempotency-Key"), q)
			respond(w, result, e)
		}
	case "reference-corrections":
		var q application.CorrectionInput
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		q.ExpectedRevision = rev
		result, e := s.App.CorrectReference(id, r.Header.Get("Idempotency-Key"), q)
		respond(w, result, e)
	case "measure-preflight":
		batch, e := decodeRounds(w, r)
		if e != nil {
			return
		}
		result, e := s.App.MeasurePreflight(id, batch)
		respond(w, result, e)
	case "evaluations":
		if len(parts) > 6 && parts[6] == "reproducibility" {
			rv, err := strconv.ParseInt(parts[5], 10, 64)
			if err != nil || rv < 1 {
				write(w, map[string]string{"error": "invalid_revision"}, 400)
				return
			}
			result, e := s.App.VerifyReproducibility(id, rv)
			respond(w, result, e)
			return
		}
		o, e1 := queryInt(r.URL.Query().Get("offset"), 0)
		l, e2 := queryInt(r.URL.Query().Get("limit"), 50)
		fr, e3 := queryInt64(r.URL.Query().Get("from_revision"), 0)
		to, e4 := queryInt64(r.URL.Query().Get("to_revision"), 0)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			write(w, map[string]string{"error": "invalid_query"}, 400)
			return
		}
		result, e := s.App.EvaluationHistory(id, o, l, fr, to)
		if e == nil && (r.URL.Query().Get("device_id") != "" || r.URL.Query().Get("metric") != "") {
			device, metric := r.URL.Query().Get("device_id"), r.URL.Query().Get("metric")
			for i := range result.Items {
				filtered := []domain.MetricAttribution{}
				for _, item := range result.Items[i].Metrics {
					if (device == "" || item.DeviceID == device) && (metric == "" || item.Metric == metric) {
						filtered = append(filtered, item)
					}
				}
				result.Items[i].Metrics = filtered
			}
		}
		respond(w, result, e)
	case "remediation-plans":
		if r.Method == "GET" {
			result, e := s.App.Plans(id, application.PlanQuery{Owner: r.URL.Query().Get("owner"), Risk: r.URL.Query().Get("risk_status")})
			respond(w, result, e)
			return
		}
		var q struct {
			Plans            []domain.RemediationPlan `json:"plans"`
			ExpectedRevision int64                    `json:"expected_revision"`
		}
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		plans, c, e := s.App.AddPlans(id, r.Header.Get("Idempotency-Key"), rev, q.Plans)
		respond(w, map[string]any{"plans": plans, "campaign": c}, e)
	case "retest-attempts":
		var q struct {
			DeviationIDs     []string                `json:"deviation_ids"`
			Retest           domain.MeasurementRound `json:"retest"`
			Round            domain.MeasurementRound `json:"round"`
			ExpectedRevision int64                   `json:"expected_revision"`
		}
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		if q.Retest.RoundID == "" {
			q.Retest = q.Round
		}
		result, e := s.App.RetestAttempt(id, r.Header.Get("Idempotency-Key"), rev, q.DeviationIDs, q.Retest)
		respond(w, result, e)
	case "archive-preflight":
		result, e := s.App.ArchivePreflight(id)
		respond(w, result, e)
	case "reference":
		var q struct {
			domain.ReferenceEvidence
			ExpectedRevision int64 `json:"expected_revision"`
		}
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		c, e := s.App.ReferenceDetailed(id, r.Header.Get("Idempotency-Key"), q.ReferenceEvidence, rev)
		respond(w, c, e)
	case "measure":
		var raw json.RawMessage
		if read(w, r, &raw) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		var q domain.MeasurementRound
		var batch []domain.MeasurementRound
		if len(raw) > 0 && raw[0] == '[' {
			if strictUnmarshal(raw, &batch) != nil {
				write(w, map[string]string{"error": "invalid_json"}, 400)
				return
			}
		} else if strictUnmarshal(raw, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		} else {
			batch = []domain.MeasurementRound{q}
		}
		c, e := s.App.MeasureIdem(id, batch, rev, r.Header.Get("Idempotency-Key"), string(raw))
		respond(w, c, e)
	case "evaluate":
		var q struct {
			ExpectedRevision int64 `json:"expected_revision"`
		}
		if r.Body != nil && r.ContentLength != 0 {
			if read(w, r, &q) != nil {
				write(w, map[string]string{"error": "invalid_json"}, 400)
				return
			}
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		result, e := s.App.EvaluateIdem(id, rev, r.Header.Get("Idempotency-Key"))
		respond(w, result, e)
	case "remediate":
		var q struct {
			Cases            []domain.DeviationCase  `json:"cases"`
			Retest           domain.MeasurementRound `json:"retest"`
			ExpectedRevision int64                   `json:"expected_revision"`
		}
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		result, e := s.App.RemediateDetailed(id, q.Cases, q.Retest, rev)
		respond(w, result, e)
	case "review":
		var q struct {
			domain.Review
			ExpectedRevision int64 `json:"expected_revision"`
		}
		if read(w, r, &q) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		if len(q.Checklist) == 0 {
			write(w, map[string]string{"error": "checklist_required"}, 400)
			return
		}
		c, e := s.App.ReviewIdem(id, q.Review, rev, r.Header.Get("Idempotency-Key"))
		respond(w, c, e)
	case "archive":
		var q struct {
			ExpectedRevision int64  `json:"expected_revision"`
			ReadinessToken   string `json:"readiness_token"`
		}
		if r.Body != nil && r.ContentLength != 0 {
			if read(w, r, &q) != nil {
				write(w, map[string]string{"error": "invalid_json"}, 400)
				return
			}
		}
		if rev == 0 {
			rev = q.ExpectedRevision
		}
		a, e := s.App.ArchiveWithToken(id, rev, q.ReadinessToken)
		respond(w, a, e)
	case "artifact":
		a, e := s.App.Store.GetArtifact(id)
		if e != nil {
			write(w, map[string]string{"error": "not_found"}, 404)
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("ETag", `"`+a.PayloadDigest+`"`)
			w.Header().Set("X-Schema-Version", a.SchemaVersion)
			w.Write(a.Payload)
		}
	case "verify":
		result := s.App.VerifySection(id, first(r.URL.Query().Get("section"), r.URL.Query().Get("section_name")))
		if result["error"] == "artifact not found" {
			write(w, map[string]string{"error": "not_found"}, 404)
		} else {
			write(w, result, 200)
		}
	case "audit":
		filter, err := decodeAuditFilter(r)
		if err != nil {
			write(w, map[string]string{"error": "invalid_audit_query"}, 400)
			return
		}
		result, e := s.App.AuditReport(id, filter)
		if e != nil {
			respond(w, nil, e)
		} else {
			write(w, result, 200)
		}
	default:
		w.WriteHeader(404)
	}
}

func (s *Server) remediationQueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	allowed := map[string]bool{"station_code": true, "device_id": true, "metric": true, "owner": true, "risk_status": true, "offset": true, "limit": true}
	for key := range q {
		if !allowed[key] {
			write(w, map[string]string{"error": "invalid_query"}, 400)
			return
		}
	}
	off, e1 := queryInt(q.Get("offset"), 0)
	lim, e2 := queryInt(q.Get("limit"), 50)
	validRisk := map[string]bool{"": true, "UNPLANNED": true, "IN_PROGRESS": true, "DUE_SOON": true, "OVERDUE": true}
	if e1 != nil || e2 != nil || off < 0 || lim < 1 || lim > 100 || !validRisk[q.Get("risk_status")] {
		write(w, map[string]string{"error": "invalid_query"}, 400)
		return
	}
	result, e := s.App.RemediationQueue(application.QueueFilter{StationCode: q.Get("station_code"), DeviceID: q.Get("device_id"), Metric: q.Get("metric"), Owner: q.Get("owner"), RiskStatus: q.Get("risk_status"), Offset: off, Limit: lim}, time.Now().UTC())
	respond(w, result, e)
}
func decodeRounds(w http.ResponseWriter, r *http.Request) ([]domain.MeasurementRound, error) {
	var raw json.RawMessage
	if read(w, r, &raw) != nil {
		write(w, map[string]string{"error": "invalid_json"}, 400)
		return nil, errors.New("invalid json")
	}
	var one domain.MeasurementRound
	var batch []domain.MeasurementRound
	if len(raw) > 0 && raw[0] == '[' {
		if strictUnmarshal(raw, &batch) != nil {
			write(w, map[string]string{"error": "invalid_json"}, 400)
			return nil, errors.New("invalid json")
		}
	} else if strictUnmarshal(raw, &one) != nil {
		write(w, map[string]string{"error": "invalid_json"}, 400)
		return nil, errors.New("invalid json")
	} else {
		batch = []domain.MeasurementRound{one}
	}
	return batch, nil
}

func decodeQualificationBatch(w http.ResponseWriter, r *http.Request) (application.QualificationBatchInput, bool, error) {
	var raw json.RawMessage
	if read(w, r, &raw) != nil || len(raw) == 0 {
		return application.QualificationBatchInput{}, false, errors.New("invalid json")
	}
	var envelope application.QualificationBatchInput
	if raw[0] == '{' {
		if strictUnmarshal(raw, &envelope) == nil && envelope.Checks != nil {
			return envelope, true, nil
		}
		// Backwards-compatible single-check body.
		var one application.QualificationBatchCheck
		if strictUnmarshal(raw, &one) != nil {
			return application.QualificationBatchInput{}, false, errors.New("invalid json")
		}
		return application.QualificationBatchInput{Checks: []application.QualificationBatchCheck{one}}, false, nil
	}
	return application.QualificationBatchInput{}, false, errors.New("invalid json")
}
func strictUnmarshal(raw []byte, value any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing json")
	}
	return nil
}
func decodeAuditFilter(r *http.Request) (application.AuditFilter, error) {
	q := r.URL.Query()
	start, err := queryInt64(q.Get("revision_start"), 0)
	if err != nil || start < 0 {
		return application.AuditFilter{}, errors.New("revision_start")
	}
	end, err := queryInt64(q.Get("revision_end"), 0)
	if err != nil || end < 0 || (end > 0 && start > end) {
		return application.AuditFilter{}, errors.New("revision_end")
	}
	offset, err := queryInt(q.Get("offset"), 0)
	if err != nil || offset < 0 {
		return application.AuditFilter{}, errors.New("offset")
	}
	limit, err := queryInt(q.Get("limit"), 50)
	if err != nil || limit < 0 || limit > 100 {
		return application.AuditFilter{}, errors.New("limit")
	}
	if limit == 0 {
		limit = 50
	}
	return application.AuditFilter{RevisionStart: start, RevisionEnd: end, Action: q.Get("action"), Actor: q.Get("actor"), Offset: offset, Limit: limit}, nil
}
func queryInt64(raw string, fallback int64) (int64, error) {
	if raw == "" {
		return fallback, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}
func respond(w http.ResponseWriter, v any, e error) {
	if e != nil {
		status := 400
		code := "invalid_input"
		if errors.Is(e, domain.ErrConflict) {
			status = 409
			code = "revision_conflict"
		}
		var conflict *domain.ConflictError
		if errors.As(e, &conflict) {
			write(w, map[string]any{"error": "resource_window_conflict", "message": e.Error(), "conflicts": conflict.Conflicts}, 409)
			return
		}
		var fingerprint *domain.CertificateFingerprintConflict
		if errors.As(e, &fingerprint) {
			write(w, map[string]any{"error": "CERTIFICATE_FINGERPRINT_CONFLICT", "conflict_fields": fingerprint.Fields}, 409)
			return
		}
		var blocked *application.RemediationBlockedError
		if errors.As(e, &blocked) {
			write(w, map[string]any{"error": "REMEDIATION_DEPENDENCY_BLOCKED", "blocking_deviation_ids": blocked.BlockingDeviationIDs}, 409)
			return
		}
		if errors.Is(e, domain.ErrDependencyCycle) {
			code, status = "DEPENDENCY_CYCLE", 422
		}
		if errors.Is(e, domain.ErrDeviationScope) {
			code, status = "DEVIATION_SCOPE_MISMATCH", 422
		}
		if errors.Is(e, domain.ErrEnvironmentFieldInvalid) {
			code, status = "ENVIRONMENT_FIELD_INVALID", 400
		}
		if errors.Is(e, domain.ErrDeviationMetricInvalid) {
			code, status = "DEVIATION_METRIC_INVALID", 400
		}
		if errors.Is(e, domain.ErrUnknownDevice) {
			code, status = "UNKNOWN_DEVICE", 400
		}
		if e.Error() == "EVALUATION_NOT_FOUND" {
			code, status = "EVALUATION_NOT_FOUND", 404
		}
		if e.Error() == "INVALID_THRESHOLD_PROFILE" {
			code, status = "INVALID_THRESHOLD_PROFILE", 422
		}
		if e.Error() == "REFERENCE_KIND_INVALID" || e.Error() == "TASK_WINDOW_INVALID" || e.Error() == "EVIDENCE_TIME_REVERSED" {
			code, status = e.Error(), 422
		}
		if errors.Is(e, domain.ErrState) {
			status = 422
			code = "invalid_state"
		}
		if errors.Is(e, domain.ErrIntegrity) {
			code, status = "integrity_verification_failed", 409
		}
		var validation *domain.ValidationError
		if errors.As(e, &validation) {
			write(w, map[string]any{"error": "measurement_plan_violation", "issues": validation.Issues}, 422)
			return
		}
		if errors.Is(e, domain.ErrDuplicate) {
			code = "duplicate_evidence"
		}
		if errors.Is(e, domain.ErrCoverage) {
			code, status = "coverage_incomplete", 422
		}
		if errors.Is(e, domain.ErrAlreadyExists) {
			code, status = "campaign_exists", 409
		}
		if errors.Is(e, sql.ErrNoRows) {
			code, status = "not_found", 404
		}
		if e.Error() == "archived" {
			code, status = "archived", 409
		}
		if e.Error() == "cancelled" {
			code, status = "cancelled", 409
		}
		if e.Error() == "unknown algorithm version" {
			code, status = "unknown_algorithm_version", 422
		}
		if e.Error() == "idempotency key conflict" {
			code, status = "idempotency_conflict", 409
		}
		if e.Error() == "stale snapshot" {
			code, status = "STALE_SNAPSHOT", 409
		}
		if e.Error() == "reviewer mismatch" {
			code, status = "REVIEWER_MISMATCH", 422
		}
		if e.Error() == "claim expired" {
			code, status = "CLAIM_EXPIRED", 409
		}
		if e.Error() == "station mismatch" {
			code, status = "STATION_MISMATCH", 422
		}
		if errors.Is(e, application.ErrReviewClaimConflict) {
			code, status = "review_claim_conflict", 409
		}
		var independence *application.ReviewerIndependenceError
		if errors.As(e, &independence) {
			write(w, map[string]any{"error": "reviewer_not_independent", "round_ids": independence.RoundIDs}, 422)
			return
		}
		if e.Error() == "retest threshold not satisfied" {
			code = "retest_threshold_failed"
		}
		if strings.Contains(e.Error(), "readiness token") || strings.Contains(e.Error(), "archive materials blocked") {
			code, status = "archive_not_ready", 409
		}
		write(w, map[string]string{"error": code, "message": e.Error()}, status)
		return
	}
	write(w, v, 200)
}
