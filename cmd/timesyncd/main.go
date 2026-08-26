package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"ground-clock-qualification/internal/application"
	"ground-clock-qualification/internal/httpapi"
	"ground-clock-qualification/internal/persistence"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:19081", "监听地址")
	data := flag.String("data", "timesync.db", "数据文件")
	self := flag.Bool("self-check", false, "执行自检")
	flag.Parse()
	if p := os.Getenv("PORT"); p != "" {
		*addr = "127.0.0.1:" + p
	}
	if h, _, e := net.SplitHostPort(*addr); e != nil || h != "127.0.0.1" {
		panic("addr must be loopback")
	}
	if *self {
		runSelf(*addr)
		return
	}
	st, e := persistence.Open(*data)
	if e != nil {
		panic(e)
	}
	defer st.Close()
	srv := &http.Server{Addr: *addr, Handler: httpapi.New(application.New(st)).Handler(), ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	fmt.Println("timesyncd listening", *addr)
	if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		panic(e)
	}
}
func runSelf(addr string) {
	dir, _ := os.MkdirTemp("", "timesync-self")
	defer os.RemoveAll(dir)
	st, e := persistence.Open(filepath.Join(dir, "x.db"))
	if e != nil {
		panic(e)
	}
	defer st.Close()
	app := application.New(st)
	srv := &http.Server{Addr: addr, Handler: httpapi.New(app).Handler()}
	ln, e := net.Listen("tcp", addr)
	if e != nil {
		panic(e)
	}
	go srv.Serve(ln)
	defer srv.Close()
	base := "http://" + addr
	start := time.Now().Add(-time.Hour).UTC()
	end := time.Now().Add(time.Hour).UTC()
	post(base+"/api/v1/campaigns", map[string]any{"campaign_id": "self", "station_code": "GS", "start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339), "created_by": "eng", "device_ids": []string{"d1"}, "threshold": map[string]any{"max_abs_deviation": 1, "max_frequency_deviation": 1, "max_drift_slope": 1}})
	post(base+"/api/v1/campaigns", map[string]any{"campaign_id": "self-page-2", "station_code": "GS2", "start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339), "created_by": "eng2", "device_ids": []string{"d2"}, "threshold": map[string]any{"max_abs_deviation": 1, "max_frequency_deviation": 1, "max_drift_slope": 1}})
	post(base+"/api/v1/campaigns/resource-availability", map[string]any{"station_code": "GS", "mission_window": map[string]any{"start": start, "end": end}, "device_ids": []string{"d1"}})
	var page1, page2 application.ListResult
	getJSON(base+"/api/v1/campaigns?limit=1&offset=0", &page1)
	getJSON(base+"/api/v1/campaigns?limit=1&offset=1", &page2)
	if page1.Total != 2 || len(page1.Campaigns) != 1 || len(page2.Campaigns) != 1 || page1.Campaigns[0].CampaignID == page2.Campaigns[0].CampaignID {
		panic("self-check pagination failed")
	}
	bad, _ := http.Get(base + "/api/v1/campaigns?limit=101")
	if bad == nil || bad.StatusCode != http.StatusBadRequest {
		panic("self-check invalid pagination failed")
	}
	bad.Body.Close()
	postWithKey(base+"/api/v1/campaigns/self/reference", map[string]any{"evidence_id": "e1", "reference_kind": "clock", "provider": "lab", "certificate_digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "valid_from": start.Format(time.RFC3339), "valid_until": end.Format(time.RFC3339), "submitted_by": "eng"}, "self-ref-clock")
	postWithKey(base+"/api/v1/campaigns/self/reference", map[string]any{"evidence_id": "e2", "reference_kind": "frequency", "provider": "lab", "certificate_digest": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "valid_from": start.Format(time.RFC3339), "valid_until": end.Format(time.RFC3339), "submitted_by": "eng"}, "self-ref-frequency")
	var digestUsage map[string]any
	getJSON(base+"/api/v1/campaigns/self/reference-digest-usage?certificate_digest=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &digestUsage)
	sampleBase := time.Now().UTC()
	for i, v := range []float64{0.2, 0.1} {
		post(base+"/api/v1/campaigns/self/measure", map[string]any{"round_id": fmt.Sprintf("r%d", i+1), "sequence": i + 1, "operator_id": "eng", "samples": []any{map[string]any{"device_id": "d1", "time_offset": v, "frequency_offset": 0, "sampled_at": sampleBase.Add(time.Duration(i) * time.Second)}}})
	}
	var consistency map[string]any
	getJSON(base+"/api/v1/campaigns/self/measurement-consistency", &consistency)
	post(base+"/api/v1/campaigns/self/evaluate", map[string]any{})
	var margins map[string]any
	getJSON(base+"/api/v1/campaigns/self/evaluation-margins", &margins)
	post(base+"/api/v1/campaigns/self/review", map[string]any{"reviewer_id": "reviewer", "approved": true, "statement": "同意签发", "checklist": []any{map[string]any{"check_code": "REFERENCE_TRACEABILITY", "result": "PASS"}, map[string]any{"check_code": "MEASUREMENT_COVERAGE", "result": "PASS"}, map[string]any{"check_code": "EVALUATION_REPRODUCIBILITY", "result": "PASS"}, map[string]any{"check_code": "REMEDIATION_CLOSURE", "result": "PASS"}}})
	post(base+"/api/v1/campaigns/self/archive", map[string]any{})
	var lineage map[string]any
	getJSON(base+"/api/v1/campaigns/self/qualification-lineage", &lineage)
	resp, _ := http.Get(base + "/api/v1/campaigns/self/artifact")
	if resp.StatusCode != 200 {
		panic("self-check artifact failed")
	}
	resp.Body.Close()
	var auditReport application.AuditResult
	getJSON(base+"/api/v1/campaigns/self/audit", &auditReport)
	var verification map[string]any
	getJSON(base+"/api/v1/campaigns/self/verify", &verification)
	if !auditReport.Integrity.Valid || verification["valid"] != true || verification["audit_head_digest"] != auditReport.Integrity.HeadDigest {
		panic("self-check integrity failed")
	}
	fmt.Println("self-check ok")
}
func post(url string, v any) {
	postWithKey(url, v, fmt.Sprintf("self-%d", time.Now().UnixNano()))
}
func postWithKey(url string, v any, key string) {
	b, _ := json.Marshal(v)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	resp, e := http.DefaultClient.Do(req)
	if e != nil || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		panic(fmt.Sprintf("request failed %s %v %s", url, e, body))
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}
func getJSON(url string, value any) {
	resp, err := http.Get(url)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		panic(fmt.Sprintf("request failed %s %s", url, body))
	}
	if err = json.NewDecoder(resp.Body).Decode(value); err != nil {
		panic(err)
	}
}

type bytesReader struct {
	b []byte
	i int
}

func (r bytesReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}
