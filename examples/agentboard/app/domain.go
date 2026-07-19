package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/abi"
)

const maxCASRetries = 16

type BoardSelector struct {
	Type    string `json:"type"`
	Project string `json:"project,omitempty"`
}

type IdentityRef struct {
	ID    string           `json:"id"`
	Kind  sdk.IdentityKind `json:"kind"`
	Owner bool             `json:"owns_app,omitempty"`
}

type Board struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Project   string   `json:"project,omitempty"`
	Name      string   `json:"name"`
	Archived  bool     `json:"archived,omitempty"`
	Members   []string `json:"member_identity_hashes,omitempty"`
	OwnerHash string   `json:"owner_identity_hash,omitempty"`
}

type Task struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	Status      string      `json:"status"`
	Creator     IdentityRef `json:"creator"`
	Updater     IdentityRef `json:"updater"`
	Sequence    uint64      `json:"sequence"`
	Revision    uint64      `json:"revision"`
}

type BoardView struct {
	Board  Board       `json:"board"`
	Caller IdentityRef `json:"caller"`
}

var (
	projectRE     = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)
	fingerprintRE = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{16,64}$`)
)

func actionError(code, message string) error { return &sdk.ActionError{Code: code, Message: message} }

func caller() (sdk.Identity, error) {
	id, err := sdk.Whoami()
	if err != nil {
		return id, actionError("identity_unavailable", "caller identity is unavailable")
	}
	if id.Kind != sdk.IdentitySSHKey || id.User == "" {
		return id, actionError("unauthorized", "a proved SSH-key identity is required")
	}
	return id, nil
}

func identityHash(user string) string   { return opaque("identity:", user) }
func boardID(kind, value string) string { return opaque("board:"+kind+":", value) }
func opaque(prefix, value string) string {
	sum := sha256.Sum256([]byte(prefix + value))
	return hex.EncodeToString(sum[:16])
}
func identityRef(id sdk.Identity) IdentityRef {
	return IdentityRef{ID: identityHash(id.User), Kind: id.Kind, Owner: id.OwnsApp}
}

func boardMetaKey(id string) string         { return "boards/" + id + "/meta" }
func boardTasksPrefix(id string) string     { return "boards/" + id + "/tasks/" }
func boardTaskKey(id, taskID string) string { return boardTasksPrefix(id) + taskID }
func boardCounterKey(id string) string      { return "boards/" + id + "/counter" }
func boardTopic(id string) string           { return "agentboard/tasks/" + id }

func loadBoard(id string) (Board, []byte, bool, error) {
	raw, ok, err := sdk.KVGet(boardMetaKey(id))
	if err != nil || !ok {
		return Board{}, raw, ok, err
	}
	var board Board
	if err := json.Unmarshal(raw, &board); err != nil {
		return Board{}, nil, false, err
	}
	return board, raw, true, nil
}

func storeBoardCAS(board Board, expected []byte) error {
	raw, err := json.Marshal(board)
	if err != nil {
		return err
	}
	var hash [sha256.Size]byte
	if expected != nil {
		hash = sdk.KVHash(expected)
	}
	return sdk.KVCompareAndSwap(boardMetaKey(board.ID), hash, raw)
}

func allBoards() ([]Board, error) {
	keys, err := sdk.KVList("boards/", abi.KVMaxList)
	if err != nil {
		return nil, err
	}
	boards := make([]Board, 0)
	for _, key := range keys {
		if !strings.HasSuffix(key, "/meta") {
			continue
		}
		raw, ok, err := sdk.KVGet(key)
		if err != nil || !ok {
			continue
		}
		var board Board
		if json.Unmarshal(raw, &board) == nil {
			boards = append(boards, board)
		}
	}
	sort.Slice(boards, func(i, j int) bool {
		if boards[i].Type != boards[j].Type {
			return boards[i].Type < boards[j].Type
		}
		return boards[i].Name < boards[j].Name
	})
	return boards, nil
}

func ensurePersonalBoard(id sdk.Identity) (Board, error) {
	hash := identityHash(id.User)
	board := Board{ID: boardID("user", hash), Type: "user", Name: "Personal", OwnerHash: hash}
	existing, _, ok, err := loadBoard(board.ID)
	if err != nil {
		return Board{}, err
	}
	if ok {
		if existing.OwnerHash != hash || existing.Type != "user" {
			return Board{}, actionError("unauthorized", "personal board ownership mismatch")
		}
		return existing, nil
	}
	if err := storeBoardCAS(board, nil); err != nil && !errors.Is(err, sdk.ErrKVConflict) {
		return Board{}, err
	}
	existing, _, _, err = loadBoard(board.ID)
	return existing, err
}

func resolveBoard(selector BoardSelector, id sdk.Identity) (Board, error) {
	switch selector.Type {
	case "user":
		if selector.Project != "" {
			return Board{}, actionError("invalid_request", "user board selector cannot target another identity")
		}
		return ensurePersonalBoard(id)
	case "project":
		if !projectRE.MatchString(selector.Project) {
			return Board{}, actionError("invalid_request", "invalid project slug")
		}
		boards, err := allBoards()
		if err != nil {
			return Board{}, err
		}
		for _, board := range boards {
			if board.Type == "project" && board.Project == selector.Project {
				if board.Archived {
					return Board{}, actionError("conflict", "project board is archived")
				}
				if id.OwnsApp || contains(board.Members, identityHash(id.User)) {
					return board, nil
				}
				return Board{}, actionError("unauthorized", "caller is not a project member")
			}
		}
		return Board{}, actionError("not_found", "project board not found")
	default:
		return Board{}, actionError("invalid_request", "board type must be project or user")
	}
}

func listVisibleBoards(id sdk.Identity) ([]Board, error) {
	personal, err := ensurePersonalBoard(id)
	if err != nil {
		return nil, err
	}
	out := []Board{personal}
	boards, err := allBoards()
	if err != nil {
		return nil, err
	}
	hash := identityHash(id.User)
	for _, board := range boards {
		if board.Type == "project" && (id.OwnsApp || (!board.Archived && contains(board.Members, hash))) {
			out = append(out, board)
		}
	}
	return out, nil
}

func listBoardTasks(board Board) ([]Task, error) {
	keys, err := sdk.KVList(boardTasksPrefix(board.ID), abi.KVMaxList)
	if err != nil {
		return nil, err
	}
	tasks := make([]Task, 0, len(keys))
	for _, key := range keys {
		raw, ok, err := sdk.KVGet(key)
		if err != nil || !ok {
			continue
		}
		var task Task
		if json.Unmarshal(raw, &task) == nil {
			tasks = append(tasks, task)
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Sequence < tasks[j].Sequence })
	return tasks, nil
}

func allocateSequence(board Board) (uint64, error) {
	key := boardCounterKey(board.ID)
	for range maxCASRetries {
		raw, ok, err := sdk.KVGet(key)
		if err != nil {
			return 0, err
		}
		var current uint64
		if ok {
			current, err = strconv.ParseUint(string(raw), 10, 64)
			if err != nil {
				return 0, err
			}
		}
		next := current + 1
		var expected [sha256.Size]byte
		if ok {
			expected = sdk.KVHash(raw)
		}
		if err := sdk.KVCompareAndSwap(key, expected, []byte(strconv.FormatUint(next, 10))); err == nil {
			return next, nil
		} else if !errors.Is(err, sdk.ErrKVConflict) {
			return 0, err
		}
	}
	return 0, actionError("conflict", "counter update contention")
}

func createTask(board Board, id sdk.Identity, title, description string) (Task, error) {
	title, description = strings.TrimSpace(title), strings.TrimSpace(description)
	if title == "" || len(title) > 160 || len(description) > 4000 {
		return Task{}, actionError("invalid_request", "title is required and task fields must fit their limits")
	}
	seq, err := allocateSequence(board)
	if err != nil {
		return Task{}, err
	}
	ref := identityRef(id)
	task := Task{ID: fmt.Sprintf("task-%06d", seq), Title: title, Description: description, Status: "pending", Creator: ref, Updater: ref, Sequence: seq, Revision: 1}
	raw, _ := json.Marshal(task)
	if err := sdk.KVCompareAndSwap(boardTaskKey(board.ID, task.ID), [sha256.Size]byte{}, raw); err != nil {
		return Task{}, err
	}
	sdk.Publish(boardTopic(board.ID), []byte(task.ID))
	return task, nil
}

type transitionActor string

const (
	actorAgent transitionActor = "agent"
	actorOwner transitionActor = "owner"
)

func nextStatus(actor transitionActor, current string) (string, error) {
	if actor == actorAgent {
		switch current {
		case "todo":
			return "in-progress", nil
		case "in-progress":
			return "in-review", nil
		}
	} else if actor == actorOwner {
		switch current {
		case "pending":
			return "todo", nil
		case "in-review":
			return "done", nil
		}
	}
	return "", actionError("conflict", "transition is not allowed for this actor")
}

func advanceTask(board Board, id sdk.Identity, taskID, expectedStatus string, actor transitionActor) (Task, error) {
	if actor == actorOwner && !id.OwnsApp {
		return Task{}, actionError("unauthorized", "only the app owner can perform human transitions")
	}
	key := boardTaskKey(board.ID, taskID)
	raw, ok, err := sdk.KVGet(key)
	if err != nil {
		return Task{}, err
	}
	if !ok {
		return Task{}, actionError("not_found", "task not found")
	}
	var task Task
	if json.Unmarshal(raw, &task) != nil {
		return Task{}, actionError("internal", "task record is invalid")
	}
	if expectedStatus == "" || task.Status != expectedStatus {
		return Task{}, actionError("conflict", "task status changed")
	}
	next, err := nextStatus(actor, task.Status)
	if err != nil {
		return Task{}, err
	}
	task.Status, task.Updater, task.Revision = next, identityRef(id), task.Revision+1
	nextRaw, _ := json.Marshal(task)
	if err := sdk.KVCompareAndSwap(key, sdk.KVHash(raw), nextRaw); err != nil {
		if errors.Is(err, sdk.ErrKVConflict) {
			return Task{}, actionError("conflict", "task changed concurrently")
		}
		return Task{}, err
	}
	sdk.Publish(boardTopic(board.ID), []byte(task.ID))
	return task, nil
}

func mutateProject(slug string, mutate func(*Board) error) (Board, error) {
	for range maxCASRetries {
		boards, err := allBoards()
		if err != nil {
			return Board{}, err
		}
		for _, board := range boards {
			if board.Type != "project" || board.Project != slug {
				continue
			}
			current, raw, ok, err := loadBoard(board.ID)
			if err != nil || !ok {
				return Board{}, err
			}
			if err := mutate(&current); err != nil {
				return Board{}, err
			}
			if err := storeBoardCAS(current, raw); err == nil {
				return current, nil
			} else if !errors.Is(err, sdk.ErrKVConflict) {
				return Board{}, err
			}
		}
		return Board{}, actionError("not_found", "project board not found")
	}
	return Board{}, actionError("conflict", "project metadata changed concurrently")
}

func createProject(id sdk.Identity, slug, name string) (Board, error) {
	if !id.OwnsApp {
		return Board{}, actionError("unauthorized", "only the app owner can create project boards")
	}
	if !projectRE.MatchString(slug) {
		return Board{}, actionError("invalid_request", "invalid project slug")
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return Board{}, actionError("invalid_request", "project name is required")
	}
	boards, err := allBoards()
	if err != nil {
		return Board{}, err
	}
	for _, board := range boards {
		if board.Type == "project" && board.Project == slug {
			return Board{}, actionError("conflict", "project slug already exists")
		}
	}
	board := Board{ID: boardID("project", slug), Type: "project", Project: slug, Name: name, Members: []string{}}
	if err := storeBoardCAS(board, nil); err != nil {
		if errors.Is(err, sdk.ErrKVConflict) {
			return Board{}, actionError("conflict", "project slug already exists")
		}
		return Board{}, err
	}
	return board, nil
}

func validateFingerprint(value string) error {
	if !fingerprintRE.MatchString(value) {
		return actionError("invalid_request", "invalid SSH SHA256 fingerprint")
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
