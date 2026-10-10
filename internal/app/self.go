package app

import (
	"context"
	"errors"
	"time"

	"github.com/usuariodejava/casamod-store/internal/github"
	"github.com/usuariodejava/casamod-store/internal/store"
)

// The CI moves the SelfTag tag to every commit it publishes as the "latest"
// image, so the tag tells which commit the newest image was built from.
const (
	SelfRepo = "UsuarioDeJava/CasaMOD-Store"
	SelfTag  = "published"

	// SelfKey is the policy key of the store itself.
	SelfKey = "store:casamod-store"
	// SelfAppID is the compose app id CasaOS knows the store by.
	SelfAppID = "casamod-store"
)

// SelfView is the store's own version as shown by the UI.
type SelfView struct {
	Version    string    `json:"version"`
	Commit     string    `json:"commit,omitempty"`
	LatestSHA  string    `json:"latest_sha,omitempty"`
	LatestDate time.Time `json:"latest_date,omitempty"`
	HasUpdate  bool      `json:"has_update"`
	LastError  string    `json:"last_error,omitempty"`
	Policy     string    `json:"policy"`
	Key        string    `json:"key"`
}

// selfHasUpdate reports whether a newer image than the running one was
// published. Builds without a commit (local "dev" builds) never report one.
func selfHasUpdate(commit string, s store.SelfUpdate) bool {
	return commit != "" && s.LatestSHA != "" && s.LatestSHA != commit
}

// selfView builds the UI view. The caller holds the state lock.
func (a *App) selfViewLocked() SelfView {
	s := a.State.Self
	return SelfView{
		Version: a.Version, Commit: a.Commit, LatestSHA: s.LatestSHA, LatestDate: s.LatestDate,
		HasUpdate: selfHasUpdate(a.Commit, s), LastError: s.LastError,
		Policy: selfPolicy(a.State.Config), Key: SelfKey,
	}
}

func selfPolicy(c store.Config) string {
	if c.SelfPolicy == store.PolicyAuto {
		return store.PolicyAuto
	}
	return store.PolicyNotify
}

// checkSelf looks for a newer image of the store. The caller holds a.op.
func (a *App) checkSelf(ctx context.Context) {
	if a.Commit == "" {
		return
	}
	run, err := a.GH.TagCommit(ctx, SelfRepo, SelfTag)
	a.State.Lock()
	a.State.Self.CheckedAt = time.Now().UTC()
	if err != nil {
		a.State.Self.LastError = err.Error()
	} else {
		was := selfHasUpdate(a.Commit, a.State.Self)
		a.State.Self = store.SelfUpdate{LatestSHA: run.SHA, LatestDate: run.Date, CheckedAt: a.State.Self.CheckedAt}
		if !was && selfHasUpdate(a.Commit, a.State.Self) {
			defer a.Logf("info", "A new version of CasaMOD Store is available (%s)", short(run.SHA))
		}
	}
	a.State.SaveLocked()
	a.State.Unlock()
	if err != nil && !errors.Is(err, github.ErrRateLimited) {
		a.Logf("error", "Checking for a new version of the store failed: %v", err)
	}
}
