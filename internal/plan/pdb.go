package plan

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"tide/internal/catalog"
)

// BlockingPDB names a PodDisruptionBudget in the service's Application that
// the target count could never satisfy, or "" when there is none.
//
// The trap it guards against: a service at 2 replicas ships a pdb.yaml with
// minAvailable 1. Scale it to 1 and the budget forbids evicting the only Pod
// there is, so the node it sits on can never be drained — while the
// Application shows Synced and Healthy throughout, because nothing failed.
// A count of 0 is fine: with no Pod there is nothing a budget can protect.
//
// It sees the budgets in the service's own Application, which is where the
// registry renders them; a budget applied by hand outside it is invisible
// here, as it is to noAutoscaler.
func BlockingPDB(ctx context.Context, c *catalog.Clients, app string, to int) (string, error) {
	if to == 0 {
		return "", nil
	}
	a, err := c.ArgoCD.GetApplication(ctx, app)
	if err != nil {
		return "", err
	}
	for _, res := range a.Status.Resources {
		if res.Kind != "PodDisruptionBudget" {
			continue
		}
		obj, err := c.ArgoCD.Resource(ctx, app, res.Group, res.Version, res.Kind, res.Namespace, res.Name)
		if err != nil {
			// Refusing on a failed lookup rather than carrying on: the point
			// is the case where the budget would pin the last Pod.
			return "", fmt.Errorf("could not read %s/%s to check whether %d replicas can be drained: %w", res.Kind, res.Name, to, err)
		}
		if PDBBlocks(obj, to) {
			return res.Name, nil
		}
	}
	return "", nil
}

// PDBBlocks reports whether a budget leaves no room to evict anything once
// the workload runs `to` Pods: minAvailable rounds up and maxUnavailable
// rounds up, the way the disruption controller computes them, and a budget
// with neither field set allows nothing at all.
func PDBBlocks(pdb map[string]any, to int) bool {
	if to <= 0 {
		return false
	}
	spec, _ := pdb["spec"].(map[string]any)
	if v, ok := spec["minAvailable"]; ok {
		return to-scaled(v, to) <= 0
	}
	if v, ok := spec["maxUnavailable"]; ok {
		return scaled(v, to) <= 0
	}
	return true
}

// scaled turns an IntOrString into a Pod count, rounding a percentage up.
func scaled(v any, total int) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	case string:
		if strings.HasSuffix(x, "%") {
			pct, err := strconv.Atoi(strings.TrimSuffix(x, "%"))
			if err != nil {
				return 0
			}
			return int(math.Ceil(float64(total) * float64(pct) / 100))
		}
		n, err := strconv.Atoi(x)
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}
