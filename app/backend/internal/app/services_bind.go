package app

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/netbox"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	maxServiceCIs     = 1000
	maxTagName        = 100
	maxTagDescription = 200
	serviceSlugPrefix = "bs-"
)

// tagColors mark the criticality of a business service on its NetBox tag.
var tagColors = map[string]string{
	model.CriticalityCritical: "f44336",
	model.CriticalityHigh:     "ff9800",
	model.CriticalityMedium:   "2196f3",
	model.CriticalityLow:      "9e9e9e",
}

// serviceSlug is the slug of the NetBox tag of a service. It follows the ID, so renaming the
// service renames the tag without breaking the link.
func serviceSlug(id string) string { return serviceSlugPrefix + strings.ToLower(id) }

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// pushTag creates or changes the NetBox tag of the service.
func (s *ServicesService) pushTag(ctx context.Context, svc *model.Service, tagID int) (*model.ServiceNetBox, error) {
	c, cfg, err := s.netbox.client()
	if err != nil {
		return nil, err
	}
	t, err := c.EnsureTag(ctx, tagID, netbox.Tag{
		Name:        clipRunes(svc.Name, maxTagName),
		Slug:        serviceSlug(svc.ID),
		Color:       tagColors[svc.Criticality],
		Description: clipRunes(svc.Description, maxTagDescription),
	})
	if err != nil {
		return nil, netboxFailure{err}
	}
	now := s.now()
	return &model.ServiceNetBox{TagID: t.ID, Slug: t.Slug, Name: t.Name, URL: cfg.TagURL(t.ID), SyncedAt: &now}, nil
}

func (s *ServicesService) deleteTag(ctx context.Context, tagID int) error {
	c, _, err := s.netbox.client()
	if err != nil {
		return err
	}
	if err := c.DeleteTag(ctx, tagID); err != nil {
		return netboxFailure{err}
	}
	return nil
}

// tagObjects puts the tag of the service on the NetBox objects of the items or takes it off.
func (s *ServicesService) tagObjects(ctx context.Context, tagID int, refs []model.NetBoxRef, on bool) error {
	if len(refs) == 0 {
		return nil
	}
	c, _, err := s.netbox.client()
	if err != nil {
		return err
	}
	for _, r := range refs {
		err := c.SetTag(ctx, r.Kind, r.ID, tagID, on)
		if errors.Is(err, netbox.ErrNotFound) {
			continue
		}
		if err != nil {
			return netboxFailure{err}
		}
	}
	return nil
}

// markTag keeps the local copy of the NetBox tags of an item in line with what was done there,
// so the next synchronization does not undo it.
func markTag(ci *model.ConfigItem, slug string, on bool) {
	if ci == nil || ci.NetBox == nil || slug == "" {
		return
	}
	has := slices.Contains(ci.Tags, slug)
	switch {
	case on && !has:
		ci.Tags = append(slices.Clone(ci.Tags), slug)
	case !on && has:
		ci.Tags = orNil(slices.DeleteFunc(slices.Clone(ci.Tags), func(x string) bool { return x == slug }))
	}
}

// BindCIs adds configuration items to the service. Items kept in NetBox also get the tag of the
// service there when the service is linked to NetBox.
func (s *ServicesService) BindCIs(ctx context.Context, actor, id string, ids []string) (ServiceView, error) {
	ids, err := serviceIDs(ids, maxServiceCIs, "too_many_cis")
	if err != nil {
		return ServiceView{}, err
	}
	var link *model.ServiceNetBox
	var refs []model.NetBoxRef
	err = ErrNotFound
	s.st.Read(func(d *store.Data) {
		svc := d.Services[id]
		if svc == nil {
			return
		}
		err, link = nil, svc.NetBox
		if len(svc.CIIDs)+len(ids) > maxServiceCIs {
			err = invalid("too_many_cis", nil)
			return
		}
		for _, ciID := range ids {
			ci := d.ConfigItems[ciID]
			if ci == nil {
				err = invalid("unknown_ci", nil)
				return
			}
			if !slices.Contains(svc.CIIDs, ciID) && ci.NetBox != nil {
				refs = append(refs, *ci.NetBox)
			}
		}
	})
	if err != nil {
		return ServiceView{}, err
	}
	if link != nil {
		if err := s.tagObjects(ctx, link.TagID, refs, true); err != nil {
			return ServiceView{}, err
		}
	}
	var out ServiceView
	err = ErrNotFound
	s.st.Write(func(d *store.Data) {
		svc := d.Services[id]
		if svc == nil {
			return
		}
		err = nil
		var added []string
		for _, ciID := range ids {
			ci := d.ConfigItems[ciID]
			if ci == nil || slices.Contains(svc.CIIDs, ciID) {
				continue
			}
			svc.CIIDs = append(svc.CIIDs, ciID)
			added = append(added, ci.Name)
			if svc.NetBox != nil {
				markTag(ci, svc.NetBox.Slug, true)
			}
		}
		if len(added) > 0 {
			svc.UpdatedAt = s.now()
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "service.bind", Object: id, Detail: svc.Name + ": " + strings.Join(added, ", ")})
		}
		out = newServiceGraph(d).view(svc)
	})
	return out, err
}

// UnbindCI removes a configuration item from the service and, in NetBox, the tag of the service
// from the object of the item.
func (s *ServicesService) UnbindCI(ctx context.Context, actor, id, ciID string) (ServiceView, error) {
	var link *model.ServiceNetBox
	var refs []model.NetBoxRef
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		svc := d.Services[id]
		if svc == nil {
			return
		}
		err, link = nil, svc.NetBox
		if ci := d.ConfigItems[ciID]; ci != nil && ci.NetBox != nil && slices.Contains(svc.CIIDs, ciID) {
			refs = append(refs, *ci.NetBox)
		}
	})
	if err != nil {
		return ServiceView{}, err
	}
	if link != nil {
		if err := s.tagObjects(ctx, link.TagID, refs, false); err != nil {
			return ServiceView{}, err
		}
	}
	var out ServiceView
	err = ErrNotFound
	s.st.Write(func(d *store.Data) {
		svc := d.Services[id]
		if svc == nil {
			return
		}
		err = nil
		if slices.Contains(svc.CIIDs, ciID) {
			svc.CIIDs = orNil(slices.DeleteFunc(slices.Clone(svc.CIIDs), func(x string) bool { return x == ciID }))
			svc.UpdatedAt = s.now()
			name := ciID
			if ci := d.ConfigItems[ciID]; ci != nil {
				name = ci.Name
				if svc.NetBox != nil {
					markTag(ci, svc.NetBox.Slug, false)
				}
			}
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "service.unbind", Object: id, Detail: svc.Name + ": " + name})
		}
		out = newServiceGraph(d).view(svc)
	})
	return out, err
}

// SetDependencies adds and removes services the service depends on, with the checks of an edit.
func (s *ServicesService) SetDependencies(actor, id string, add, remove []string) (ServiceView, error) {
	var out ServiceView
	err := ErrNotFound
	s.st.Write(func(d *store.Data) {
		svc := d.Services[id]
		if svc == nil {
			return
		}
		deps := slices.Clone(svc.DependsOn)
		deps = append(deps, add...)
		deps = slices.DeleteFunc(deps, func(x string) bool { return slices.Contains(remove, x) })
		in := ServiceInput{Name: svc.Name, Description: svc.Description, OwnerTeamID: svc.OwnerTeamID, TeamIDs: svc.TeamIDs,
			Criticality: svc.Criticality, Status: svc.Status, Tags: svc.Tags, Links: svc.Links, DependsOn: deps}
		var spec ServiceInput
		if spec, err = in.normalize(); err != nil {
			return
		}
		if err = spec.check(d, id); err != nil {
			return
		}
		if !slices.Equal(spec.DependsOn, svc.DependsOn) {
			svc.DependsOn = spec.DependsOn
			svc.UpdatedAt = s.now()
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "service.update", Object: id, Detail: svc.Name + ": dependencies"})
		}
		out = newServiceGraph(d).view(svc)
	})
	return out, err
}

// LinkNetBox creates the tag of the service in NetBox (or takes over the one with its slug)
// and puts it on the NetBox objects of the bound items. From then on the tag decides which
// NetBox items belong to the service.
func (s *ServicesService) LinkNetBox(ctx context.Context, actor, id string) (ServiceView, error) {
	var cur model.Service
	var refs []model.NetBoxRef
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		if svc := d.Services[id]; svc != nil {
			cur, err = *svc, nil
			for _, ciID := range svc.CIIDs {
				if ci := d.ConfigItems[ciID]; ci != nil && ci.NetBox != nil {
					refs = append(refs, *ci.NetBox)
				}
			}
		}
	})
	if err != nil {
		return ServiceView{}, err
	}
	tagID := 0
	if cur.NetBox != nil {
		tagID = cur.NetBox.TagID
	}
	link, err := s.pushTag(ctx, &cur, tagID)
	if err != nil {
		return ServiceView{}, err
	}
	if err := s.tagObjects(ctx, link.TagID, refs, true); err != nil {
		return ServiceView{}, err
	}
	var out ServiceView
	err = ErrNotFound
	s.st.Write(func(d *store.Data) {
		svc := d.Services[id]
		if svc == nil {
			return
		}
		err = nil
		svc.NetBox = link
		for _, ciID := range svc.CIIDs {
			markTag(d.ConfigItems[ciID], link.Slug, true)
		}
		d.AddAudit(store.AuditEntry{Actor: actor, Action: "service.netbox", Object: id,
			Detail: svc.Name + ": linked to NetBox tag " + link.Slug + " (" + strconv.Itoa(link.TagID) + ")"})
		out = newServiceGraph(d).view(svc)
	})
	return out, err
}

// UnlinkNetBox deletes the tag of the service in NetBox. The bindings stay in Umbrella.
func (s *ServicesService) UnlinkNetBox(ctx context.Context, actor, id string) (ServiceView, error) {
	var link *model.ServiceNetBox
	err := ErrNotFound
	s.st.Read(func(d *store.Data) {
		if svc := d.Services[id]; svc != nil {
			link, err = svc.NetBox, nil
		}
	})
	if err != nil {
		return ServiceView{}, err
	}
	if link != nil {
		if err := s.deleteTag(ctx, link.TagID); err != nil {
			return ServiceView{}, err
		}
	}
	var out ServiceView
	err = ErrNotFound
	s.st.Write(func(d *store.Data) {
		svc := d.Services[id]
		if svc == nil {
			return
		}
		err = nil
		if svc.NetBox != nil {
			for _, ci := range d.ConfigItems {
				markTag(ci, svc.NetBox.Slug, false)
			}
			svc.NetBox = nil
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "service.netbox", Object: id, Detail: svc.Name + ": unlinked from NetBox"})
		}
		out = newServiceGraph(d).view(svc)
	})
	return out, err
}

// dropCI removes a deleted configuration item from every service.
func dropCI(d *store.Data, id string) {
	dropHostLinks(d, id)
	for _, svc := range d.Services {
		if slices.Contains(svc.CIIDs, id) {
			svc.CIIDs = orNil(slices.DeleteFunc(slices.Clone(svc.CIIDs), func(x string) bool { return x == id }))
		}
	}
}

// applyServiceTags follows the NetBox tags of linked services after a synchronization: items
// read from NetBox with the tag of a service belong to it, items without it do not. A service
// whose tag is gone from NetBox is unlinked and keeps its bindings, unless the answer is held as
// incomplete (keep).
func applyServiceTags(d *store.Data, cfg netbox.Config, tags []netbox.Tag, seen map[string]bool, now time.Time, stats *model.SyncStats, keep bool) {
	byID, bySlug := map[int]netbox.Tag{}, map[string]netbox.Tag{}
	for _, t := range tags {
		byID[t.ID], bySlug[strings.ToLower(t.Slug)] = t, t
	}
	ids := make([]string, 0, len(d.Services))
	for id := range d.Services {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		svc := d.Services[id]
		if svc.NetBox == nil {
			continue
		}
		t, ok := byID[svc.NetBox.TagID]
		if !ok || !strings.EqualFold(t.Slug, svc.NetBox.Slug) {
			t, ok = bySlug[strings.ToLower(svc.NetBox.Slug)]
		}
		if !ok && keep {
			continue
		}
		if !ok {
			svc.NetBox = nil
			stats.ServiceUnlinked++
			d.AddAudit(store.AuditEntry{Actor: netboxActor, Action: "service.netbox", Object: id, Detail: svc.Name + ": tag is gone from NetBox, unlinked"})
			continue
		}
		at := now
		svc.NetBox = &model.ServiceNetBox{TagID: t.ID, Slug: strings.ToLower(t.Slug), Name: t.Name, URL: cfg.TagURL(t.ID), SyncedAt: &at}
		changed := false
		for _, ci := range sortedCIs(d) {
			if ci.NetBox == nil || !seen[refKey(ci.NetBox.Kind, ci.NetBox.ID)] {
				continue
			}
			tagged, bound := slices.Contains(ci.Tags, svc.NetBox.Slug), slices.Contains(svc.CIIDs, ci.ID)
			switch {
			case tagged && !bound:
				svc.CIIDs = append(svc.CIIDs, ci.ID)
				stats.ServiceBound++
				changed = true
			case !tagged && bound:
				svc.CIIDs = orNil(slices.DeleteFunc(slices.Clone(svc.CIIDs), func(x string) bool { return x == ci.ID }))
				stats.ServiceUnbound++
				changed = true
			}
		}
		if changed {
			svc.UpdatedAt = now
		}
	}
}

func sortedCIs(d *store.Data) []*model.ConfigItem {
	out := make([]*model.ConfigItem, 0, len(d.ConfigItems))
	for _, ci := range d.ConfigItems {
		out = append(out, ci)
	}
	slices.SortFunc(out, func(a, b *model.ConfigItem) int { return strings.Compare(a.ID, b.ID) })
	return out
}
