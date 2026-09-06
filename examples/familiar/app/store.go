package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/Ceinl/plumtree/sdk/kv"
)

// message is one conversation turn, both as stored in KV and as sent upstream.
type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

const (
	convMaxMessages = 24
	convMaxBytes    = 56 << 10 // kv values cap at 64 KiB; leave headroom
	memMax          = 12
	memMaxRunes     = 200
	nameMaxRunes    = 32
)

func convKey(uid string) string { return "familiar/conv/" + uid }
func memKey(uid string) string  { return "familiar/mem/" + uid }
func nameKey(uid string) string { return "familiar/name/" + uid }

// uidFor derives a stable, bounded storage key from a session identity. SSH
// fingerprints are stable across logins; anonymous ids are per-session, so
// anonymous visitors intentionally get ephemeral conversations.
func uidFor(user string) string {
	sum := sha256.Sum256([]byte(user))
	return hex.EncodeToString(sum[:6])
}

func kvGet(ctx context.Context, key string) ([]byte, bool) {
	result := kv.Get(key).Run(ctx)
	if result.Err != nil || !result.Found {
		return nil, false
	}
	return result.Value, true
}

func loadConversation(ctx context.Context, uid string) []message {
	raw, ok := kvGet(ctx, convKey(uid))
	if !ok {
		return nil
	}
	var history []message
	if json.Unmarshal(raw, &history) != nil {
		return nil
	}
	return history
}

// saveConversation persists a conversation, dropping the oldest turns until
// the payload fits both bounds.
func saveConversation(ctx context.Context, uid string, history []message) error {
	if len(history) > convMaxMessages {
		history = history[len(history)-convMaxMessages:]
	}
	for {
		raw, err := json.Marshal(history)
		if err != nil {
			return err
		}
		if len(raw) <= convMaxBytes || len(history) <= 2 {
			return kv.Set(convKey(uid), raw).Run(ctx).Err
		}
		history = history[1:]
	}
}

func clearConversation(ctx context.Context, uid string) error {
	return kv.Delete(convKey(uid)).Run(ctx).Err
}

func loadMemories(ctx context.Context, uid string) []string {
	raw, ok := kvGet(ctx, memKey(uid))
	if !ok {
		return nil
	}
	var memories []string
	if json.Unmarshal(raw, &memories) != nil {
		return nil
	}
	trimmed := make([]string, 0, len(memories))
	for _, memory := range memories {
		if memory = strings.TrimSpace(memory); memory != "" {
			trimmed = append(trimmed, truncateRunes(memory, memMaxRunes))
		}
	}
	return trimmed
}

func saveMemories(ctx context.Context, uid string, memories []string) error {
	raw, err := json.Marshal(memories)
	if err != nil {
		return err
	}
	return kv.Set(memKey(uid), raw).Run(ctx).Err
}

func loadName(ctx context.Context, uid string) string {
	raw, ok := kvGet(ctx, nameKey(uid))
	if !ok {
		return ""
	}
	return truncateRunes(strings.TrimSpace(string(raw)), nameMaxRunes)
}

func saveName(ctx context.Context, uid, name string) error {
	return kv.Set(nameKey(uid), []byte(name)).Run(ctx).Err
}
