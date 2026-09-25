package experiments

// Results mirrors the analysis service's results document. Sample is set only
// when Studio generated the document itself because no service is configured.
type Results struct {
	ExperimentKey string          `json:"experiment_key"`
	AsOf          string          `json:"as_of"`
	Status        string          `json:"status"`
	Message       *string         `json:"message"`
	Unit          string          `json:"unit"`
	Method        Method          `json:"method"`
	Variants      []VariantUnits  `json:"variants"`
	SRM           SRM             `json:"srm"`
	Metrics       []MetricResult  `json:"metrics"`
	Segments      []SegmentResult `json:"segments"`
	Timeseries    []TimePoint     `json:"timeseries"`
	Diagnostics   []Diagnostic    `json:"diagnostics"`
	Decision      *Recommendation `json:"decision"`
	Sample        bool            `json:"sample,omitempty"`
}

type Method struct {
	Test       string  `json:"test"`
	Alpha      float64 `json:"alpha"`
	CUPED      bool    `json:"cuped"`
	Correction string  `json:"correction"`
	// SequentialTuning is the per-variant tuning of the sequential test, or null.
	SequentialTuning map[string]float64 `json:"sequential_tuning,omitempty"`
}

type VariantUnits struct {
	Key           string  `json:"key"`
	IsControl     bool    `json:"is_control"`
	Units         int64   `json:"units"`
	ExpectedShare float64 `json:"expected_share"`
}

type SRM struct {
	Chi2            float64 `json:"chi2"`
	PValue          float64 `json:"p_value"`
	Flag            bool    `json:"flag"`
	MaxAbsDeviation float64 `json:"max_abs_deviation"`
}

type MetricResult struct {
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Role      string    `json:"role"`
	Direction string    `json:"direction"`
	Format    string    `json:"format"`
	Results   []ArmStat `json:"results"`
}

// ArmStat is one variant against control. When CUPED applies, the top-level
// estimates are the CUPED-adjusted ones and Raw holds the unadjusted readout;
// Raw and CUPED are both null otherwise. Lift, interval and p-values are null
// when relative lift is undefined (control mean <= 0).
type ArmStat struct {
	Variant      string          `json:"variant"`
	Value        float64         `json:"value"`
	ControlValue float64         `json:"control_value"`
	Lift         *float64        `json:"lift"`
	CILow        *float64        `json:"ci_low"`
	CIHigh       *float64        `json:"ci_high"`
	PValue       *float64        `json:"p_value"`
	AdjustedP    *float64        `json:"adjusted_p"`
	Significant  bool            `json:"significant"`
	Raw          *RawStat        `json:"raw"`
	CUPED        *CUPEDStat      `json:"cuped"`
	Guardrail    *GuardrailCheck `json:"guardrail"`
}

// RawStat is the unadjusted readout when the top level is CUPED-adjusted.
type RawStat struct {
	Value        float64  `json:"value"`
	ControlValue float64  `json:"control_value"`
	Lift         *float64 `json:"lift"`
	CILow        *float64 `json:"ci_low"`
	CIHigh       *float64 `json:"ci_high"`
	PValue       *float64 `json:"p_value"`
}

type CUPEDStat struct {
	Value             float64  `json:"value"`
	Lift              *float64 `json:"lift"`
	CILow             *float64 `json:"ci_low"`
	CIHigh            *float64 `json:"ci_high"`
	VarianceReduction float64  `json:"variance_reduction"`
}

// GuardrailCheck may carry only Pass and Reason when there is no usable data.
type GuardrailCheck struct {
	MaxDropPct      *float64 `json:"max_drop_pct,omitempty"`
	Pass            bool     `json:"pass"`
	SignificantHarm *bool    `json:"significant_harm,omitempty"`
	Reason          string   `json:"reason,omitempty"`
}

func num(x float64) *float64 { return &x }

// val reads an optional estimate; ok is false when it is null.
func val(p *float64) (float64, bool) {
	if p == nil {
		return 0, false
	}
	return *p, true
}

type SegmentResult struct {
	Dimension string         `json:"dimension"`
	Value     string         `json:"value"`
	Metrics   []MetricResult `json:"metrics"`
}

type TimePoint struct {
	Date    string  `json:"date"`
	Metric  string  `json:"metric"`
	Variant string  `json:"variant"`
	Lift    float64 `json:"lift"`
	CILow   float64 `json:"ci_low"`
	CIHigh  float64 `json:"ci_high"`
}

type Diagnostic struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type Recommendation struct {
	Recommendation string `json:"recommendation"`
	Variant        string `json:"variant,omitempty"`
	Reason         string `json:"reason"`
}
