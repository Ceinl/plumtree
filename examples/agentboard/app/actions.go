package main

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/Ceinl/plumtree/sdk"
)

func decodeArgs(raw json.RawMessage, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return actionError("invalid_request", "invalid action arguments")
	}
	return nil
}

func appActions() sdk.Actions {
	return sdk.Actions{
		"get_identity": func(_ sdk.Ctx, raw json.RawMessage) (any, error) {
			var args struct{}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			id, err := caller()
			if err != nil {
				return nil, err
			}
			return map[string]any{"identity": id, "identity_ref": identityRef(id)}, nil
		},
		"list_boards": func(_ sdk.Ctx, raw json.RawMessage) (any, error) {
			var args struct{}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			id, err := caller()
			if err != nil {
				return nil, err
			}
			boards, err := listVisibleBoards(id)
			return map[string]any{"caller": identityRef(id), "boards": boards}, err
		},
		"list_tasks": func(_ sdk.Ctx, raw json.RawMessage) (any, error) {
			var args struct {
				Board BoardSelector `json:"board"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			id, err := caller()
			if err != nil {
				return nil, err
			}
			board, err := resolveBoard(args.Board, id)
			if err != nil {
				return nil, err
			}
			tasks, err := listBoardTasks(board)
			return map[string]any{"caller": identityRef(id), "board": board, "tasks": tasks}, err
		},
		"create_task": func(_ sdk.Ctx, raw json.RawMessage) (any, error) {
			var args struct {
				Board       BoardSelector `json:"board"`
				Title       string        `json:"title"`
				Description string        `json:"description"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			id, err := caller()
			if err != nil {
				return nil, err
			}
			board, err := resolveBoard(args.Board, id)
			if err != nil {
				return nil, err
			}
			task, err := createTask(board, id, args.Title, args.Description)
			return map[string]any{"caller": identityRef(id), "board": board, "task": task}, err
		},
		"advance_task": func(_ sdk.Ctx, raw json.RawMessage) (any, error) {
			var args struct {
				Board          BoardSelector `json:"board"`
				TaskID         string        `json:"task_id"`
				ExpectedStatus string        `json:"expected_status"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			id, err := caller()
			if err != nil {
				return nil, err
			}
			board, err := resolveBoard(args.Board, id)
			if err != nil {
				return nil, err
			}
			actor := actorAgent
			if board.Type == "user" {
				actor = actorPersonal
			}
			task, err := advanceTask(board, id, args.TaskID, args.ExpectedStatus, actor)
			return map[string]any{"caller": identityRef(id), "board": board, "task": task}, err
		},
		"create_project_board": func(_ sdk.Ctx, raw json.RawMessage) (any, error) {
			var args struct {
				Project string `json:"project"`
				Name    string `json:"name"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			id, err := caller()
			if err != nil {
				return nil, err
			}
			board, err := createProject(id, args.Project, args.Name)
			return map[string]any{"caller": identityRef(id), "board": board}, err
		},
		"add_project_member":    projectMemberAction(true),
		"remove_project_member": projectMemberAction(false),
		"rename_project_board": func(_ sdk.Ctx, raw json.RawMessage) (any, error) {
			var args struct {
				Project string `json:"project"`
				Name    string `json:"name"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			id, err := caller()
			if err != nil {
				return nil, err
			}
			if !id.OwnsApp {
				return nil, actionError("unauthorized", "only the app owner can rename projects")
			}
			name := strings.TrimSpace(args.Name)
			if name == "" || len(name) > 80 {
				return nil, actionError("invalid_request", "project name is required")
			}
			board, err := mutateProject(args.Project, func(board *Board) error { board.Name = name; return nil })
			return map[string]any{"caller": identityRef(id), "board": board}, err
		},
		"archive_project_board": func(_ sdk.Ctx, raw json.RawMessage) (any, error) {
			var args struct {
				Project  string `json:"project"`
				Archived bool   `json:"archived"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			id, err := caller()
			if err != nil {
				return nil, err
			}
			if !id.OwnsApp {
				return nil, actionError("unauthorized", "only the app owner can archive projects")
			}
			board, err := mutateProject(args.Project, func(board *Board) error { board.Archived = args.Archived; return nil })
			return map[string]any{"caller": identityRef(id), "board": board}, err
		},
	}
}

func projectMemberAction(add bool) sdk.ActionHandler {
	return func(_ sdk.Ctx, raw json.RawMessage) (any, error) {
		var args struct {
			Project  string `json:"project"`
			Identity string `json:"identity"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		id, err := caller()
		if err != nil {
			return nil, err
		}
		if !id.OwnsApp {
			return nil, actionError("unauthorized", "only the app owner can manage project members")
		}
		if err := validateFingerprint(args.Identity); err != nil {
			return nil, err
		}
		hash := identityHash(args.Identity)
		board, err := mutateProject(args.Project, func(board *Board) error {
			if add {
				if !contains(board.Members, hash) {
					board.Members = append(board.Members, hash)
				}
			} else {
				out := board.Members[:0]
				for _, member := range board.Members {
					if member != hash {
						out = append(out, member)
					}
				}
				board.Members = out
			}
			return nil
		})
		return map[string]any{"caller": identityRef(id), "board": board}, err
	}
}
