package store

type refChange struct {
	target   *string
	from, to string
}

func (d *Data) secretRefs() []*string {
	return []*string{&d.Settings.LDAP.BindPasswordRef, &d.Settings.APITokenKeyRef}
}

func (s *Store) MapRefs(move func(ref string) (string, bool)) (int, func()) {
	var changes []refChange
	s.Write(func(d *Data) {
		for _, ref := range d.secretRefs() {
			if *ref == "" {
				continue
			}
			if next, ok := move(*ref); ok && next != *ref {
				changes = append(changes, refChange{target: ref, from: *ref, to: next})
				*ref = next
			}
		}
	})
	undo := func() {
		s.Write(func(d *Data) {
			for _, c := range changes {
				if *c.target == c.to {
					*c.target = c.from
				}
			}
		})
	}
	return len(changes), undo
}
