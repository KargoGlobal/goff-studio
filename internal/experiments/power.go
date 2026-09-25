package experiments

import (
	"fmt"
	"math"
)

type PowerRequest struct {
	BaselineMean float64 `json:"baseline_mean"`
	Variance     float64 `json:"variance"`
	NPerDay      float64 `json:"n_per_day"`
	Arms         int     `json:"arms"`
	Alpha        float64 `json:"alpha"`
	Power        float64 `json:"power"`
	CUPEDRho2    float64 `json:"cuped_rho2"`
	Days         int     `json:"days,omitempty"`
	TargetMDE    float64 `json:"target_mde,omitempty"`
}

type PowerPoint struct {
	Days int     `json:"days"`
	MDE  float64 `json:"mde"`
}

type PowerResult struct {
	MDE       float64      `json:"mde"`
	Days      int          `json:"days"`
	DaysToMDE *int         `json:"days_to_mde"`
	NPerArm   float64      `json:"n_per_arm"`
	Curve     []PowerPoint `json:"curve"`
	Source    string       `json:"source,omitempty"`
}

func (r PowerRequest) Validate() error {
	var p Problems
	if r.BaselineMean == 0 {
		p.add("the baseline mean cannot be zero, lift is relative to it")
	}
	if r.Variance <= 0 {
		p.add("the variance must be positive")
	}
	if r.NPerDay <= 0 {
		p.add("daily units must be positive")
	}
	if r.Arms < 2 {
		p.add("an experiment has at least two arms")
	}
	if r.Alpha <= 0 || r.Alpha >= 0.5 {
		p.add("alpha must be between 0 and 0.5")
	}
	if r.Power <= 0 || r.Power >= 1 {
		p.add("power must be between 0 and 1")
	}
	if r.CUPEDRho2 < 0 || r.CUPEDRho2 >= 1 {
		p.add("the CUPED variance reduction must be at least 0 and below 1")
	}
	return p.Err()
}

// Estimate is a two-sample z-test power calculation on relative lift, used
// when no analysis service is configured.
func Estimate(r PowerRequest) (PowerResult, error) {
	if err := r.Validate(); err != nil {
		return PowerResult{}, err
	}
	days := r.Days
	if days <= 0 {
		days = 28
	}
	z := normalQuantile(1-r.Alpha/2) + normalQuantile(r.Power)
	varAdj := r.Variance * (1 - r.CUPEDRho2)
	baseline := math.Abs(r.BaselineMean)

	mdeAt := func(d int) float64 {
		n := r.NPerDay * float64(d) / float64(r.Arms)
		return z * math.Sqrt(2*varAdj/n) / baseline
	}

	out := PowerResult{
		MDE:     round(mdeAt(days), 6),
		Days:    days,
		NPerArm: math.Round(r.NPerDay * float64(days) / float64(r.Arms)),
		Source:  "local",
	}
	for d := 7; d <= 56; d += 7 {
		out.Curve = append(out.Curve, PowerPoint{Days: d, MDE: round(mdeAt(d), 6)})
	}
	if r.TargetMDE > 0 {
		need := 2 * varAdj * math.Pow(z/(r.TargetMDE*baseline), 2)
		d := int(math.Ceil(need * float64(r.Arms) / r.NPerDay))
		if d < 1 {
			d = 1
		}
		out.DaysToMDE = &d
	}
	return out, nil
}

func (r PowerResult) String() string {
	return fmt.Sprintf("MDE %.2f%% at %d days", r.MDE*100, r.Days)
}
