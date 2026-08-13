// Package kv provides typed scoped durable key/value operations for both
// interactive and finite SDK applications. Authority is the app's private
// durable namespace; operation results are copied values with no retained
// handles. Native uses an isolated in-process adapter, while hosted selection
// keeps the same bounds and stable errors behind the selected host capability.
package kv

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	legacy "github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/internal/operation"
)

var (
	ErrUnavailable = errors.New("kv: capability unavailable")
	ErrInvalid     = errors.New("kv: invalid request")
	ErrTooLarge    = errors.New("kv: request too large")
	ErrQuota       = errors.New("kv: storage quota exceeded")
	ErrConflict    = errors.New("kv: compare-and-swap conflict")
)

// GetResult contains a copied value and its presence state.
type GetResult struct {
	Value []byte
	Found bool
	Err   error
}

// SetResult reports a set operation.
type SetResult struct{ Err error }

// DeleteResult reports a delete operation.
type DeleteResult struct{ Err error }

// ListResult contains lexicographically ordered keys.
type ListResult struct {
	Keys []string
	Err  error
}

// CompareAndSwapResult reports an atomic write result.
type CompareAndSwapResult struct{ Err error }

// GetOperation is a finite typed KV read. Run executes it; Map converts its
// completion to an interactive app event.
type GetOperation struct {
	inner operation.Operation[GetResult]
}

func (op GetOperation) Run(ctx context.Context) GetResult { return op.inner.Run(ctx) }
func (op GetOperation) Map(mapper func(GetResult) app.Event) app.Command {
	return op.inner.Map(mapper)
}
func (op GetOperation) Ignore() app.Command { return op.inner.Ignore() }

// SetOperation is a finite typed KV write.
type SetOperation struct {
	inner operation.Operation[SetResult]
}

func (op SetOperation) Run(ctx context.Context) SetResult { return op.inner.Run(ctx) }
func (op SetOperation) Map(mapper func(SetResult) app.Event) app.Command {
	return op.inner.Map(mapper)
}
func (op SetOperation) Ignore() app.Command { return op.inner.Ignore() }

// DeleteOperation is a finite typed KV delete.
type DeleteOperation struct {
	inner operation.Operation[DeleteResult]
}

func (op DeleteOperation) Run(ctx context.Context) DeleteResult { return op.inner.Run(ctx) }
func (op DeleteOperation) Map(mapper func(DeleteResult) app.Event) app.Command {
	return op.inner.Map(mapper)
}
func (op DeleteOperation) Ignore() app.Command { return op.inner.Ignore() }

// ListOperation is a finite typed KV listing.
type ListOperation struct {
	inner operation.Operation[ListResult]
}

func (op ListOperation) Run(ctx context.Context) ListResult { return op.inner.Run(ctx) }
func (op ListOperation) Map(mapper func(ListResult) app.Event) app.Command {
	return op.inner.Map(mapper)
}
func (op ListOperation) Ignore() app.Command { return op.inner.Ignore() }

// CompareAndSwapOperation is a finite typed atomic KV write.
type CompareAndSwapOperation struct {
	inner operation.Operation[CompareAndSwapResult]
}

func (op CompareAndSwapOperation) Run(ctx context.Context) CompareAndSwapResult {
	return op.inner.Run(ctx)
}
func (op CompareAndSwapOperation) Map(mapper func(CompareAndSwapResult) app.Event) app.Command {
	return op.inner.Map(mapper)
}
func (op CompareAndSwapOperation) Ignore() app.Command { return op.inner.Ignore() }

// Get returns an inert read operation.
func Get(key string) GetOperation {
	return GetOperation{inner: operation.New(func(ctx context.Context) GetResult {
		if err := validateKey(key); err != nil {
			return GetResult{Err: err}
		}
		if err := ctx.Err(); err != nil {
			return GetResult{Err: err}
		}
		var value []byte
		var found bool
		var err error
		if adapter := adapterFor(ctx); adapter != nil {
			value, found, err = adapter.Get(key)
		} else {
			value, found, err = legacy.KVGet(key)
		}
		return GetResult{Value: append([]byte(nil), value...), Found: found, Err: normalize(err)}
	})}
}

// Set returns an inert write operation.
func Set(key string, value []byte) SetOperation {
	copyValue := append([]byte(nil), value...)
	return SetOperation{inner: operation.New(func(ctx context.Context) SetResult {
		if err := validateKeyValue(key, copyValue); err != nil {
			return SetResult{Err: err}
		}
		if err := ctx.Err(); err != nil {
			return SetResult{Err: err}
		}
		if adapter := adapterFor(ctx); adapter != nil {
			return SetResult{Err: normalize(adapter.Set(key, copyValue))}
		}
		return SetResult{Err: normalize(legacy.KVSet(key, copyValue))}
	})}
}

// Delete returns an inert delete operation.
func Delete(key string) DeleteOperation {
	return DeleteOperation{inner: operation.New(func(ctx context.Context) DeleteResult {
		if err := validateKey(key); err != nil {
			return DeleteResult{Err: err}
		}
		if err := ctx.Err(); err != nil {
			return DeleteResult{Err: err}
		}
		if adapter := adapterFor(ctx); adapter != nil {
			return DeleteResult{Err: normalize(adapter.Delete(key))}
		}
		return DeleteResult{Err: normalize(legacy.KVDelete(key))}
	})}
}

// List returns an inert bounded key listing operation.
func List(prefix string, limit int) ListOperation {
	return ListOperation{inner: operation.New(func(ctx context.Context) ListResult {
		if len(prefix) > abi.KVMaxKey || limit > abi.KVMaxList {
			return ListResult{Err: ErrTooLarge}
		}
		if limit < 1 {
			return ListResult{Err: ErrInvalid}
		}
		if err := ctx.Err(); err != nil {
			return ListResult{Err: err}
		}
		var keys []string
		var err error
		if adapter := adapterFor(ctx); adapter != nil {
			keys, err = adapter.List(prefix, limit)
		} else {
			keys, err = legacy.KVList(prefix, limit)
		}
		return ListResult{Keys: append([]string(nil), keys...), Err: normalize(err)}
	})}
}

// CompareAndSwap returns an inert atomic compare-and-swap operation.
func CompareAndSwap(key string, expected [sha256.Size]byte, value []byte) CompareAndSwapOperation {
	copyValue := append([]byte(nil), value...)
	return CompareAndSwapOperation{inner: operation.New(func(ctx context.Context) CompareAndSwapResult {
		if err := validateKeyValue(key, copyValue); err != nil {
			return CompareAndSwapResult{Err: err}
		}
		if err := ctx.Err(); err != nil {
			return CompareAndSwapResult{Err: err}
		}
		if adapter := adapterFor(ctx); adapter != nil {
			return CompareAndSwapResult{Err: normalize(adapter.CompareAndSwap(key, expected, copyValue))}
		}
		return CompareAndSwapResult{Err: normalize(legacy.KVCompareAndSwap(key, expected, copyValue))}
	})}
}

// Hash returns the revision expected by CompareAndSwap.
func Hash(value []byte) [sha256.Size]byte { return legacy.KVHash(value) }

func validateKey(key string) error {
	if len(key) == 0 {
		return ErrInvalid
	}
	if len(key) > abi.KVMaxKey {
		return ErrTooLarge
	}
	return nil
}

func validateKeyValue(key string, value []byte) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if len(value) > abi.KVMaxValue {
		return ErrTooLarge
	}
	return nil
}

func normalize(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, legacy.ErrKVUnavailable):
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	case errors.Is(err, legacy.ErrKVTooLarge):
		return fmt.Errorf("%w: %v", ErrTooLarge, err)
	case errors.Is(err, legacy.ErrKVQuota):
		return fmt.Errorf("%w: %v", ErrQuota, err)
	case errors.Is(err, legacy.ErrKVConflict):
		return fmt.Errorf("%w: %v", ErrConflict, err)
	default:
		return err
	}
}

// Compile-time references keep the app conversion visible in package docs and
// ensure this slice remains coupled to the clean interactive lifecycle.
var _ func(func(GetResult) app.Event) app.Command = Get("").Map
