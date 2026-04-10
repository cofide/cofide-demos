package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

// JWTSVIDSource fetches and caches a JWT-SVID for a given audience, refreshing
// it when it is within one minute of expiry.
type JWTSVIDSource struct {
	wlClient *workloadapi.Client
	audience string
	mu       sync.Mutex
	svid     *jwtsvid.SVID
}

// GetSVID returns a valid JWT-SVID, fetching a new one from the workload API when
// the cached SVID is absent or within one minute of expiry.
func (s *JWTSVIDSource) GetSVID(ctx context.Context) (*jwtsvid.SVID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.svid == nil || time.Until(s.svid.Expiry) < time.Minute {
		slog.Info("Fetching JWT-SVID")
		svid, err := s.wlClient.FetchJWTSVID(ctx, jwtsvid.Params{Audience: s.audience})
		if err != nil {
			if s.svid != nil && time.Now().Before(s.svid.Expiry) {
				slog.Warn("Failed to refresh JWT-SVID, using cached SVID", "error", err)
				return s.svid, nil
			}
			return nil, fmt.Errorf("failed to obtain JWT-SVID: %w", err)
		}
		s.svid = svid
		slog.Info("Fetched JWT-SVID", "id", svid.ID.String(), "expiry", svid.Expiry.Format(time.RFC3339))
	} else {
		slog.Debug("Using cached JWT-SVID", "id", s.svid.ID.String(), "expiry", s.svid.Expiry.Format(time.RFC3339))
	}
	return s.svid, nil
}
