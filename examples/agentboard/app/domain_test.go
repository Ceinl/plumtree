package main

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/abi"
)

const (
	memberFingerprint = "SHA256:AbCdEf0123456789AbCdEf0123456789AbCdEf01234"
	otherFingerprint  = "SHA256:ZyXwVu9876543210ZyXwVu9876543210ZyXwVu98765"
)

func cleanStore(t *testing.T) {
	t.Helper()
	keys, _ := sdk.KVList("boards/", abi.KVMaxList)
	for _, key := range keys {
		_ = sdk.KVDelete(key)
	}
}

func ownerIdentity() sdk.Identity {
	return sdk.Identity{User: "owner-key", Kind: sdk.IdentitySSHKey, Authenticated: true, OwnsApp: true}
}
func memberIdentity() sdk.Identity {
	return sdk.Identity{User: memberFingerprint, Kind: sdk.IdentitySSHKey}
}

func seedProject(t *testing.T) Board {
	t.Helper()
	project, err := createProject(ownerIdentity(), "alpha", "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	_, err = mutateProject("alpha", func(board *Board) error {
		board.Members = append(board.Members, identityHash(memberFingerprint))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return project
}

func TestProjectAndPersonalAuthorization(t *testing.T) {
	cleanStore(t)
	t.Cleanup(func() { cleanStore(t) })
	project := seedProject(t)
	member := memberIdentity()
	got, err := resolveBoard(BoardSelector{Type: "project", Project: "alpha"}, member)
	if err != nil || got.ID != project.ID {
		t.Fatalf("member project = %+v, %v", got, err)
	}
	if _, err := resolveBoard(BoardSelector{Type: "project", Project: "alpha"}, sdk.Identity{User: otherFingerprint, Kind: sdk.IdentitySSHKey}); err == nil {
		t.Fatal("unlisted key accessed project")
	}
	personalMember, err := resolveBoard(BoardSelector{Type: "user"}, member)
	if err != nil {
		t.Fatal(err)
	}
	personalOwner, err := resolveBoard(BoardSelector{Type: "user"}, ownerIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if personalMember.ID == personalOwner.ID {
		t.Fatal("personal boards collided")
	}
	if _, err := resolveBoard(BoardSelector{Type: "user", Project: memberFingerprint}, ownerIdentity()); err == nil {
		t.Fatal("targeted personal selector accepted")
	}
	raw, _, _, _ := loadBoard(project.ID)
	encoded, _ := json.Marshal(raw)
	if strings.Contains(string(encoded), memberFingerprint) {
		t.Fatal("raw fingerprint persisted in project metadata")
	}
}

func TestWorkflowAndIndependentCounters(t *testing.T) {
	cleanStore(t)
	t.Cleanup(func() { cleanStore(t) })
	project := seedProject(t)
	member := memberIdentity()
	projectTask, err := createTask(project, member, "Project task", "")
	if err != nil || projectTask.Status != "pending" || projectTask.ID != "task-000001" {
		t.Fatalf("project task = %+v, %v", projectTask, err)
	}
	personal, _ := ensurePersonalBoard(member)
	personalTask, err := createTask(personal, member, "Personal task", "")
	if err != nil || personalTask.ID != "task-000001" {
		t.Fatalf("personal task = %+v, %v", personalTask, err)
	}
	for _, status := range []string{"pending", "todo", "in-progress", "in-review"} {
		personalTask, err = advanceTask(personal, member, personalTask.ID, status, actorPersonal)
		if err != nil {
			t.Fatalf("personal transition from %s: %v", status, err)
		}
	}
	if personalTask.Status != "done" {
		t.Fatalf("personal task status = %q, want done", personalTask.Status)
	}
	if _, err := advanceTask(personal, sdk.Identity{User: otherFingerprint, Kind: sdk.IdentitySSHKey}, personalTask.ID, "done", actorPersonal); err == nil {
		t.Fatal("another identity advanced a personal task")
	}
	projectTask, err = advanceTask(project, ownerIdentity(), projectTask.ID, "pending", actorOwner)
	if err != nil || projectTask.Status != "todo" {
		t.Fatalf("owner transition = %+v, %v", projectTask, err)
	}
	projectTask, err = advanceTask(project, member, projectTask.ID, "todo", actorAgent)
	if err != nil || projectTask.Status != "in-progress" {
		t.Fatalf("agent transition = %+v, %v", projectTask, err)
	}
	if _, err := advanceTask(project, member, projectTask.ID, "in-progress", actorOwner); err == nil {
		t.Fatal("non-owner performed human transition")
	}
}

func TestConcurrentAdvanceExactlyOneWins(t *testing.T) {
	cleanStore(t)
	t.Cleanup(func() { cleanStore(t) })
	project := seedProject(t)
	task, _ := createTask(project, memberIdentity(), "Concurrent", "")
	task, _ = advanceTask(project, ownerIdentity(), task.ID, "pending", actorOwner)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := advanceTask(project, memberIdentity(), task.ID, "todo", actorAgent)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
			continue
		}
		var actionErr *sdk.ActionError
		if errors.As(err, &actionErr) && actionErr.Code == "conflict" {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	tasks, _ := listBoardTasks(project)
	if len(tasks) != 1 || tasks[0].Status != "in-progress" {
		t.Fatalf("final tasks = %+v", tasks)
	}
}

func TestConcurrentUpdatesOnDifferentBoardsBothSucceed(t *testing.T) {
	cleanStore(t)
	t.Cleanup(func() { cleanStore(t) })
	alpha := seedProject(t)
	beta, err := createProject(ownerIdentity(), "beta", "Beta")
	if err != nil {
		t.Fatal(err)
	}
	member := memberIdentity()
	alphaTask, err := createTask(alpha, member, "Alpha task", "")
	if err != nil {
		t.Fatal(err)
	}
	betaTask, err := createTask(beta, member, "Beta task", "")
	if err != nil {
		t.Fatal(err)
	}
	alphaTask, _ = advanceTask(alpha, ownerIdentity(), alphaTask.ID, "pending", actorOwner)
	betaTask, _ = advanceTask(beta, ownerIdentity(), betaTask.ID, "pending", actorOwner)

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, update := range []struct {
		board Board
		task  Task
	}{{alpha, alphaTask}, {beta, betaTask}} {
		update := update
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := advanceTask(update.board, member, update.task.ID, "todo", actorAgent)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("independent board update failed: %v", err)
		}
	}
	for _, board := range []Board{alpha, beta} {
		tasks, err := listBoardTasks(board)
		if err != nil || len(tasks) != 1 || tasks[0].Status != "in-progress" {
			t.Fatalf("board %s tasks = %+v, %v", board.Project, tasks, err)
		}
	}
}

func TestAnonymousRejectedByActions(t *testing.T) {
	t.Setenv("PLUMTREE_IDENTITY_USER", "anonymous:test")
	t.Setenv("PLUMTREE_IDENTITY_KIND", string(sdk.IdentityAnonymous))
	t.Setenv("PLUMTREE_IDENTITY_AUTHENTICATED", "false")
	t.Setenv("PLUMTREE_IDENTITY_OWNS_APP", "false")
	_, err := appActions()["list_boards"](sdk.Ctx{}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("anonymous action accepted")
	}
}
