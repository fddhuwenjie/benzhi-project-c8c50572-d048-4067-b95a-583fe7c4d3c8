package domain

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
)

// EnvironmentCorrelationIssue identifies a value that could not be paired for analysis.
type EnvironmentCorrelationIssue struct {
	Code     string `json:"code"`
	RoundID  string `json:"round_id"`
	Sequence int    `json:"sequence"`
	DeviceID string `json:"device_id"`
	Field    string `json:"field"`
	Message  string `json:"message"`
}

type EnvironmentCorrelationItem struct {
	DeviceID           string                        `json:"device_id"`
	EnvironmentField   string                        `json:"environment_field"`
	DeviationMetric    string                        `json:"deviation_metric"`
	ValidPairs         int                           `json:"valid_pairs"`
	SampleCount        int                           `json:"sample_count"`
	InvalidSamples     int                           `json:"invalid_samples"`
	MissingCount       int                           `json:"missing_count"`
	MeanEnvironment    float64                       `json:"mean_environment"`
	MeanDeviation      float64                       `json:"mean_deviation"`
	PearsonCorrelation float64                       `json:"pearson_correlation"`
	Slope              float64                       `json:"slope"`
	Conclusion         string                        `json:"conclusion"`
	Issues             []EnvironmentCorrelationIssue `json:"issues"`
}

type EnvironmentCorrelation struct {
	AnalyzedRevision int64                        `json:"analyzed_revision"`
	ValidPairs       int                          `json:"valid_pairs"`
	ValidSamples     int                          `json:"valid_samples"`
	InvalidSamples   int                          `json:"invalid_samples"`
	MissingCount     int                          `json:"missing_count"`
	Results          []EnvironmentCorrelationItem `json:"results"`
}

var (
	ErrEnvironmentFieldInvalid = errors.New("ENVIRONMENT_FIELD_INVALID")
	ErrDeviationMetricInvalid  = errors.New("DEVIATION_METRIC_INVALID")
	ErrUnknownDevice           = errors.New("UNKNOWN_DEVICE")
)

// BuildEnvironmentCorrelation computes deterministic environment/deviation statistics.
func BuildEnvironmentCorrelation(c *Campaign, rounds []MeasurementRound, deviceID, environmentField, deviationMetric string) (EnvironmentCorrelation, error) {
	if c == nil {
		return EnvironmentCorrelation{}, ErrInvalid
	}
	if environmentField != "" && environmentField != "temperature" && environmentField != "humidity" {
		return EnvironmentCorrelation{}, ErrEnvironmentFieldInvalid
	}
	if deviationMetric != "" && deviationMetric != "time_deviation" && deviationMetric != "frequency_deviation" {
		return EnvironmentCorrelation{}, ErrDeviationMetricInvalid
	}
	if deviceID != "" {
		found := false
		for _, id := range c.DeviceIDs {
			if id == deviceID {
				found = true
				break
			}
		}
		if !found {
			return EnvironmentCorrelation{}, ErrUnknownDevice
		}
	}
	fields := []string{"temperature", "humidity"}
	metrics := []string{"time_deviation", "frequency_deviation"}
	if environmentField != "" {
		fields = []string{environmentField}
	}
	if deviationMetric != "" {
		metrics = []string{deviationMetric}
	}
	devices := append([]string(nil), c.DeviceIDs...)
	if deviceID != "" {
		devices = []string{deviceID}
	}
	type pair struct{ x, y float64 }
	type bucket struct {
		pairs   []pair
		invalid int
		missing int
		issues  []EnvironmentCorrelationIssue
	}
	buckets := map[string]*bucket{}
	for _, d := range devices {
		for _, f := range fields {
			for _, m := range metrics {
				buckets[d+"|"+f+"|"+m] = &bucket{}
			}
		}
	}
	knownDevices := map[string]bool{}
	for _, d := range devices {
		knownDevices[d] = true
	}
	orderedRounds := append([]MeasurementRound(nil), rounds...)
	sort.SliceStable(orderedRounds, func(i, j int) bool {
		if orderedRounds[i].Sequence != orderedRounds[j].Sequence {
			return orderedRounds[i].Sequence < orderedRounds[j].Sequence
		}
		return orderedRounds[i].RoundID < orderedRounds[j].RoundID
	})
	for _, r := range orderedRounds {
		samples := append([]Sample(nil), r.Samples...)
		sort.SliceStable(samples, func(i, j int) bool {
			if samples[i].DeviceID != samples[j].DeviceID {
				return samples[i].DeviceID < samples[j].DeviceID
			}
			return samples[i].SampledAt.Before(samples[j].SampledAt)
		})
		for _, sample := range samples {
			if !knownDevices[sample.DeviceID] {
				continue
			}
			for _, f := range fields {
				raw, present := r.Environment[f]
				x, xerr := strconv.ParseFloat(strings.TrimSpace(raw), 64)
				if !present || strings.TrimSpace(raw) == "" || xerr != nil || math.IsNaN(x) || math.IsInf(x, 0) {
					for _, m := range metrics {
						b := buckets[sample.DeviceID+"|"+f+"|"+m]
						b.invalid++
						code := "ENVIRONMENT_MISSING"
						if present && strings.TrimSpace(raw) != "" {
							code = "ENVIRONMENT_NOT_FINITE"
						} else {
							b.missing++
						}
						b.issues = append(b.issues, EnvironmentCorrelationIssue{code, r.RoundID, r.Sequence, sample.DeviceID, f, "环境值缺失或不是有限数值"})
					}
					continue
				}
				for _, m := range metrics {
					y := sample.TimeOffset
					if m == "frequency_deviation" {
						y = sample.FrequencyOffset
					}
					b := buckets[sample.DeviceID+"|"+f+"|"+m]
					if math.IsNaN(y) || math.IsInf(y, 0) {
						b.invalid++
						b.issues = append(b.issues, EnvironmentCorrelationIssue{"DEVIATION_NOT_FINITE", r.RoundID, r.Sequence, sample.DeviceID, m, "偏差值不是有限数值"})
						continue
					}
					b.pairs = append(b.pairs, pair{x, y})
				}
			}
		}
	}
	out := EnvironmentCorrelation{AnalyzedRevision: c.Revision, Results: []EnvironmentCorrelationItem{}}
	for _, d := range devices {
		for _, f := range fields {
			for _, m := range metrics {
				b := buckets[d+"|"+f+"|"+m]
				item := EnvironmentCorrelationItem{DeviceID: d, EnvironmentField: f, DeviationMetric: m, ValidPairs: len(b.pairs), SampleCount: len(b.pairs), InvalidSamples: b.invalid, MissingCount: b.missing, Issues: append([]EnvironmentCorrelationIssue(nil), b.issues...)}
				if len(b.pairs) < 3 {
					item.Conclusion = "INSUFFICIENT_DATA"
				} else {
					var sx, sy float64
					for _, p := range b.pairs {
						sx += p.x
						sy += p.y
					}
					mx, my := sx/float64(len(b.pairs)), sy/float64(len(b.pairs))
					item.MeanEnvironment, item.MeanDeviation = roundCorrelation(mx), roundCorrelation(my)
					var cov, vx, vy float64
					for _, p := range b.pairs {
						dx, dy := p.x-mx, p.y-my
						cov += dx * dy
						vx += dx * dx
						vy += dy * dy
					}
					if vx > 0 && vy > 0 {
						item.PearsonCorrelation = roundCorrelation(cov / math.Sqrt(vx*vy))
					} else {
						item.PearsonCorrelation = 0
					}
					if vx > 0 {
						item.Slope = roundCorrelation(cov / vx)
					}
					absr := math.Abs(item.PearsonCorrelation)
					if absr >= 0.8 {
						item.Conclusion = "STRONG"
					} else if absr >= 0.5 {
						item.Conclusion = "MODERATE"
					} else {
						item.Conclusion = "WEAK"
					}
				}
				sort.Slice(item.Issues, func(i, j int) bool {
					if item.Issues[i].Sequence != item.Issues[j].Sequence {
						return item.Issues[i].Sequence < item.Issues[j].Sequence
					}
					if item.Issues[i].RoundID != item.Issues[j].RoundID {
						return item.Issues[i].RoundID < item.Issues[j].RoundID
					}
					if item.Issues[i].DeviceID != item.Issues[j].DeviceID {
						return item.Issues[i].DeviceID < item.Issues[j].DeviceID
					}
					return item.Issues[i].Code < item.Issues[j].Code
				})
				out.ValidPairs += item.ValidPairs
				out.ValidSamples += item.ValidPairs
				out.InvalidSamples += item.InvalidSamples
				out.MissingCount += item.MissingCount
				out.Results = append(out.Results, item)
			}
		}
	}
	sort.Slice(out.Results, func(i, j int) bool {
		a, b := out.Results[i], out.Results[j]
		if a.DeviceID != b.DeviceID {
			return a.DeviceID < b.DeviceID
		}
		if a.EnvironmentField != b.EnvironmentField {
			return a.EnvironmentField < b.EnvironmentField
		}
		return a.DeviationMetric < b.DeviationMetric
	})
	return out, nil
}

func roundCorrelation(v float64) float64 {
	if v == 0 {
		return 0
	}
	return math.Round(v*1e9) / 1e9
}
