package settings

import (
	"context"
	"errors"
)

// loadOr loads section into a copy of def. An unsaved section yields def;
// keys missing from a saved section keep their default values.
func loadOr[T any](ctx context.Context, s *Store, section string, def T) (T, error) {
	v := def
	if err := s.Load(ctx, section, &v); err != nil {
		if errors.Is(err, ErrNotConfigured) {
			return def, nil
		}
		return def, err
	}
	return v, nil
}

func (s *Store) Security(ctx context.Context) (Security, error) {
	return loadOr(ctx, s, SectionSecurity, DefaultSecurity())
}

func (s *Store) ReleasePolicy(ctx context.Context) (ReleasePolicy, error) {
	p, err := loadOr(ctx, s, SectionRelease, DefaultReleasePolicy())
	if p.ReasonRequired == nil {
		p.ReasonRequired = []string{}
	}
	if p.JiraRequired == nil {
		p.JiraRequired = []string{}
	}
	if p.Approvals == nil {
		p.Approvals = []ApprovalPolicy{}
	}
	if p.SoakEnforced == nil {
		p.SoakEnforced = []string{}
	}
	if p.ConfigDriftEnforced == nil {
		p.ConfigDriftEnforced = []string{}
	}
	if p.VersionJumpEnforced == nil {
		p.VersionJumpEnforced = []string{}
	}
	if p.JiraProjects == nil {
		p.JiraProjects = []string{}
	}
	if p.Freezes == nil {
		p.Freezes = []Freeze{}
	}
	return p, err
}

func (s *Store) System(ctx context.Context) (System, error) {
	return loadOr(ctx, s, SectionSystem, DefaultSystem())
}

func (s *Store) Notify(ctx context.Context) (Notify, error) {
	n, err := loadOr(ctx, s, SectionNotify, DefaultNotify())
	if n.Channels == nil {
		n.Channels = []Channel{}
	}
	if n.Rules == nil {
		n.Rules = []NotifyRule{}
	}
	return n, err
}

func (s *Store) Catalog(ctx context.Context) (Catalog, error) {
	c, err := loadOr(ctx, s, SectionCatalog, DefaultCatalog())
	if c.Dimensions == nil {
		c.Dimensions = []Dimension{}
	}
	for i := range c.Dimensions {
		if c.Dimensions[i].Values == nil {
			c.Dimensions[i].Values = []DimensionValue{}
		}
	}
	return c, err
}

func (s *Store) Environments(ctx context.Context) (Environments, error) {
	e, err := loadOr(ctx, s, SectionEnvironments, Environments{Items: []Environment{}})
	if e.Items == nil {
		e.Items = []Environment{}
	}
	return e, err
}
