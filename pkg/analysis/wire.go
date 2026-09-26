package analysis

import "math"

// PowerRequestV1 is the request body of POST /v1/experiments/power. The wire
// works in percentages where Studio works in fractions.
type PowerRequestV1 struct {
	BaselineMean float64 `json:"baseline_mean"`
	Variance     float64 `json:"variance"`
	NPerDay      float64 `json:"n_per_day"`
	Arms         int     `json:"arms"`
	Alpha        float64 `json:"alpha"`
	Power        float64 `json:"power"`
	CUPEDRho2    float64 `json:"cuped_rho2"`
	Days         int     `json:"days,omitempty"`
	TargetMDEPct float64 `json:"target_mde_pct,omitempty"`
}

type PowerPointV1 struct {
	Days   int     `json:"days"`
	MDEPct float64 `json:"mde_pct"`
}

// PowerResultV1 is the response body of POST /v1/experiments/power.
type PowerResultV1 struct {
	Days         int            `json:"days"`
	NPerArm      float64        `json:"n_per_arm"`
	MDEPct       float64        `json:"mde_pct"`
	ByWeek       []PowerPointV1 `json:"mde_by_week"`
	DaysToTarget *int           `json:"days_to_target"`
}

func (r PowerRequest) V1() PowerRequestV1 {
	return PowerRequestV1{
		BaselineMean: r.BaselineMean,
		Variance:     r.Variance,
		NPerDay:      r.NPerDay,
		Arms:         r.Arms,
		Alpha:        r.Alpha,
		Power:        r.Power,
		CUPEDRho2:    r.CUPEDRho2,
		Days:         r.Days,
		TargetMDEPct: r.TargetMDE * 100,
	}
}

func (r PowerRequestV1) Request() PowerRequest {
	return PowerRequest{
		BaselineMean: r.BaselineMean,
		Variance:     r.Variance,
		NPerDay:      r.NPerDay,
		Arms:         r.Arms,
		Alpha:        r.Alpha,
		Power:        r.Power,
		CUPEDRho2:    r.CUPEDRho2,
		Days:         r.Days,
		TargetMDE:    r.TargetMDEPct / 100,
	}
}

// Result converts to Studio's shape; Source is left for the caller to set.
func (r PowerResultV1) Result() PowerResult {
	out := PowerResult{
		MDE:       r.MDEPct / 100,
		Days:      r.Days,
		DaysToMDE: r.DaysToTarget,
		NPerArm:   math.Round(r.NPerArm),
		Curve:     []PowerPoint{},
	}
	for _, p := range r.ByWeek {
		out.Curve = append(out.Curve, PowerPoint{Days: p.Days, MDE: p.MDEPct / 100})
	}
	return out
}

func (r PowerResult) V1() PowerResultV1 {
	out := PowerResultV1{
		Days:         r.Days,
		NPerArm:      r.NPerArm,
		MDEPct:       r.MDE * 100,
		ByWeek:       []PowerPointV1{},
		DaysToTarget: r.DaysToMDE,
	}
	for _, p := range r.Curve {
		out.ByWeek = append(out.ByWeek, PowerPointV1{Days: p.Days, MDEPct: p.MDE * 100})
	}
	return out
}
