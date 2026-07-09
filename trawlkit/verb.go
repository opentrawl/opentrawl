package trawlkit

import (
	"context"
	"flag"
	"time"
)

type Verb struct {
	Name    string
	Help    string
	Args    []string
	Flags   func(fs *flag.FlagSet)
	Mutates bool
	// Secondary keeps a verb out of the headline verb list, moving it under a
	// "More verbs" heading. It is for the specialist verbs a reader reaches for
	// rarely, so the main list stays about the everyday ones.
	Secondary bool
	// Store declares archive access. StoreDefault keeps the runner default.
	Store   StoreAccess
	Timeout time.Duration
	Run     func(ctx context.Context, req *Request) error
}

// StoreAccess declares how a verb opens the archive store.
type StoreAccess int

const (
	// StoreDefault keeps the verb's runner default.
	StoreDefault StoreAccess = iota
	// StoreNone runs without opening or creating an archive.
	StoreNone
	// StoreOptional opens the archive read-only when it exists. It is only
	// valid on non-mutating verbs.
	StoreOptional
	// StoreRequired opens a bespoke verb's archive, read-only for
	// non-mutating verbs and read-write for mutating verbs.
	StoreRequired
)
