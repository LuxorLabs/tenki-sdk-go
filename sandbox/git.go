package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
)

// GitOperation is the structured git operation name.
type GitOperation string

const (
	GitClone    GitOperation = "clone"
	GitCheckout GitOperation = "checkout"
	GitDiff     GitOperation = "diff"
	GitLog      GitOperation = "log"
)

// Git is a scoped helper for structured git operations on one session.
type Git struct {
	session *Session
}

// GitCloneParams controls clone behavior.
type GitCloneParams struct {
	Branch    string
	Depth     int
	Directory string
}

// GitCheckoutParams controls checkout behavior.
type GitCheckoutParams struct {
	Create bool
}

// GitDiffParams controls diff behavior.
type GitDiffParams struct {
	Range string
	Base  string
	Head  string
	Path  string
}

// GitLogParams controls log behavior.
type GitLogParams struct {
	MaxCount int
	Range    string
	Path     string
}

// GitFetchPRParams controls fetch+checkout behavior for pull requests.
type GitFetchPRParams struct {
	Directory    string
	Remote       string
	BranchPrefix string
}

// Clone executes git clone with structured params.
func (g *Git) Clone(ctx context.Context, repo string, params GitCloneParams) (string, error) {
	args := map[string]string{
		"repo": repo,
	}
	if params.Branch != "" {
		args["branch"] = params.Branch
	}
	if params.Depth > 0 {
		args["depth"] = strconv.Itoa(params.Depth)
	}
	if params.Directory != "" {
		args["directory"] = params.Directory
	}
	return g.session.GitOperation(ctx, GitClone, args)
}

// Checkout executes git checkout with structured params.
func (g *Git) Checkout(ctx context.Context, ref string, params GitCheckoutParams) (string, error) {
	args := map[string]string{
		"ref": ref,
	}
	if params.Create {
		args["create"] = "true"
	}
	return g.session.GitOperation(ctx, GitCheckout, args)
}

// Diff executes git diff with structured params.
func (g *Git) Diff(ctx context.Context, params GitDiffParams) (string, error) {
	args := map[string]string{}
	if params.Range != "" {
		args["range"] = params.Range
	}
	if params.Base != "" {
		args["base"] = params.Base
	}
	if params.Head != "" {
		args["head"] = params.Head
	}
	if params.Path != "" {
		args["path"] = params.Path
	}
	return g.session.GitOperation(ctx, GitDiff, args)
}

// Log executes git log with structured params.
func (g *Git) Log(ctx context.Context, params GitLogParams) (string, error) {
	args := map[string]string{}
	if params.MaxCount > 0 {
		args["max_count"] = strconv.Itoa(params.MaxCount)
	}
	if params.Range != "" {
		args["range"] = params.Range
	}
	if params.Path != "" {
		args["path"] = params.Path
	}
	return g.session.GitOperation(ctx, GitLog, args)
}

// FetchPR fetches pull/<number>/head into a local branch and checks it out.
func (g *Git) FetchPR(ctx context.Context, prNumber int, params GitFetchPRParams) (string, error) {
	if prNumber <= 0 {
		return "", errors.New("sandbox: pr number must be > 0")
	}
	remote := strings.TrimSpace(params.Remote)
	if remote == "" {
		remote = "origin"
	}
	branchPrefix := strings.TrimSpace(params.BranchPrefix)
	if branchPrefix == "" {
		branchPrefix = "pr"
	}
	branch := fmt.Sprintf("%s-%d", branchPrefix, prNumber)
	refspec := fmt.Sprintf("pull/%d/head:%s", prNumber, branch)

	baseArgs := []string{}
	if dir := strings.TrimSpace(params.Directory); dir != "" {
		baseArgs = append(baseArgs, "-C", dir)
	}

	fetchArgs := append(append([]string{}, baseArgs...), "fetch", remote, refspec)
	fetchResult, err := g.session.Exec(ctx, "git", WithArgs(fetchArgs...))
	if err != nil {
		return "", err
	}
	if fetchResult.Status != CommandStatusSucceeded {
		return "", fmt.Errorf(
			"sandbox: git fetch PR failed (status=%s exit_code=%d): %s",
			fetchResult.Status,
			fetchResult.ExitCode,
			strings.TrimSpace(string(fetchResult.Stderr)),
		)
	}

	checkoutArgs := append(append([]string{}, baseArgs...), "checkout", branch)
	checkoutResult, err := g.session.Exec(ctx, "git", WithArgs(checkoutArgs...))
	if err != nil {
		return "", err
	}
	if checkoutResult.Status != CommandStatusSucceeded {
		return "", fmt.Errorf(
			"sandbox: git checkout PR failed (status=%s exit_code=%d): %s",
			checkoutResult.Status,
			checkoutResult.ExitCode,
			strings.TrimSpace(string(checkoutResult.Stderr)),
		)
	}

	output := strings.TrimSpace(string(fetchResult.Stdout))
	checkoutOutput := strings.TrimSpace(string(checkoutResult.Stdout))
	if output == "" {
		return checkoutOutput, nil
	}
	if checkoutOutput == "" {
		return output, nil
	}
	return output + "\n" + checkoutOutput, nil
}

// GitOperation runs one structured git operation inside a sandbox session.
// Deprecated: prefer session.Git.Clone/Checkout/Diff/Log for better ergonomics.
func (s *Session) GitOperation(ctx context.Context, operation GitOperation, args map[string]string) (string, error) {
	resp, err := s.client.sandbox.GitOperation(ctx, connect.NewRequest(&sandboxv1.GitOperationRequest{
		SessionId: s.ID,
		Operation: string(operation),
		Args:      cloneStringMap(args),
	}))
	if err != nil {
		return "", mapError(err)
	}
	return resp.Msg.Output, nil
}
