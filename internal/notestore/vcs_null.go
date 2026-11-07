package notestore

import "sync"

// NullVCS is the null_git test double (IMPLEMENTATION-FRAMEWORK.md stub
// inventory): it records commits instead of running git, so store tests
// assert the contract (which paths, which message) without a repository.
type NullVCS struct {
	mu      sync.Mutex
	Inits   int
	Commits []Commit
}

type Commit struct {
	Paths   []string
	Message string
}

func (v *NullVCS) Init() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.Inits++
	return nil
}

func (v *NullVCS) CommitPaths(paths []string, message string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.Commits = append(v.Commits, Commit{Paths: append([]string{}, paths...), Message: message})
	return nil
}

// NullIndex records index notifications without a database.
type NullIndex struct {
	mu      sync.Mutex
	Upserts []*Note
	Removed []string
}

func (i *NullIndex) Upsert(n *Note) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.Upserts = append(i.Upserts, n)
	return nil
}

func (i *NullIndex) Remove(id string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.Removed = append(i.Removed, id)
	return nil
}
