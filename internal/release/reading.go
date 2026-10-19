package release

// The confirmation sheet is held for a reading time so that a click cannot
// land before the person has seen what the sheet warns about. A release that
// warns about nothing — an upgrade with no anomaly and no configuration
// riding along, or a restart — has nothing to hold the click for; the wait
// there was only friction, and it taught people to wait without reading.

// notices are anomalies that are worth knowing but not worth stopping for:
// the sheet shows them apart from the warnings. Everything else — including
// any code added later — counts as a warning, so a new kind of anomaly is
// read before it is confirmed until someone decides otherwise.
var notices = map[string]bool{AnomalyFirstDeploy: true, AnomalyFirstDeployPerKargo: true}

// NeedsReading reports whether the release's confirmation must wait for the
// reading time: some item carries a warning, or changes more than an image.
func (r *Release) NeedsReading() bool {
	for _, it := range r.Items {
		switch it.Kind {
		case KindRestart:
			continue // same version, same configuration
		case KindImage:
			p, err := r.ImagePayload(it)
			if err != nil || len(p.ConfigChanges) > 0 {
				return true
			}
			for _, a := range p.Anomalies {
				if !notices[a.Code] {
					return true
				}
			}
		default:
			// A config sync is nothing but changes; a scale changes what
			// runs. Both, and any kind added later, are read first.
			return true
		}
	}
	return false
}
