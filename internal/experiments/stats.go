package experiments

import "math"

// normalQuantile is the inverse standard normal CDF (Acklam's rational approximation, |error| < 1.2e-9 after refinement).
func normalQuantile(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	a := []float64{-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02, 1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00}
	b := []float64{-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02, 6.680131188771972e+01, -1.328068155288572e+01}
	c := []float64{-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00, -2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00}
	d := []float64{7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00, 3.754408661907416e+00}

	const low = 0.02425
	var x float64
	switch {
	case p < low:
		q := math.Sqrt(-2 * math.Log(p))
		x = (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) / ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p <= 1-low:
		q := p - 0.5
		r := q * q
		x = (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q / (((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	default:
		q := math.Sqrt(-2 * math.Log(1-p))
		x = -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) / ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	}

	e := normalCDF(x) - p
	u := e * math.Sqrt(2*math.Pi) * math.Exp(x*x/2)
	return x - u/(1+x*u/2)
}

func normalCDF(x float64) float64 {
	return 0.5 * math.Erfc(-x/math.Sqrt2)
}

func twoSidedP(z float64) float64 {
	return 2 * (1 - normalCDF(math.Abs(z)))
}

// chiSquareSurvival approximates P(X > x) for X ~ chi2(df) with Wilson-Hilferty.
func chiSquareSurvival(x float64, df int) float64 {
	if df <= 0 {
		return 1
	}
	if x <= 0 {
		return 1
	}
	k := float64(df)
	z := (math.Cbrt(x/k) - (1 - 2/(9*k))) / math.Sqrt(2/(9*k))
	return 1 - normalCDF(z)
}

// SRMCheck runs a chi-square goodness-of-fit test of observed units against expected shares.
func SRMCheck(units []int64, shares []float64) SRM {
	var total int64
	for _, u := range units {
		total += u
	}
	if total == 0 || len(units) != len(shares) || len(units) < 2 {
		return SRM{PValue: 1}
	}
	var chi2, maxDev float64
	for i, u := range units {
		expected := shares[i] * float64(total)
		if expected <= 0 {
			continue
		}
		diff := float64(u) - expected
		chi2 += diff * diff / expected
		if dev := math.Abs(float64(u)/float64(total) - shares[i]); dev > maxDev {
			maxDev = dev
		}
	}
	p := chiSquareSurvival(chi2, len(units)-1)
	return SRM{Chi2: round(chi2, 4), PValue: round(p, 6), Flag: p < 0.001, MaxAbsDeviation: round(maxDev, 6)}
}

func round(x float64, places int) float64 {
	pow := math.Pow(10, float64(places))
	return math.Round(x*pow) / pow
}
