// Copyright 2021 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package rotation

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/exposure-notifications-server/pkg/logging"
	"github.com/google/exposure-notifications-verification-server/pkg/observability"
	"github.com/hashicorp/go-multierror"
	"go.opencensus.io/stats"
	"go.opencensus.io/tag"
)

// HandleRotate handles key rotation.
func (c *Controller) HandleRotate() http.Handler {
	type Result struct {
		OK     bool    `json:"ok"`
		Errors []error `json:"errors,omitempty"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		logger := logging.FromContext(ctx).Named("rotation.HandleRotate")

		var merr *multierror.Error

		ok, err := c.shouldRotate(ctx)
		if err != nil {
			logger.Errorw("failed to run shouldRotate", "error", err)
			c.h.RenderJSON(w, http.StatusInternalServerError, &Result{
				OK:     false,
				Errors: []error{err},
			})
			return
		}
		if !ok {
			c.h.RenderJSON(w, http.StatusOK, &Result{
				OK:     false,
				Errors: []error{fmt.Errorf("too early")},
			})
			return
		}

		// Token signing keys
		func() {
			item := tag.Upsert(itemTagKey, "TOKEN_SIGNING_KEYS")
			result := observability.ResultOK()
			defer observability.RecordLatency(ctx, time.Now(), mLatencyMs, &result, &item)

			existing, err := c.db.ActiveTokenSigningKey()
			if err != nil {
				merr = multierror.Append(merr, fmt.Errorf("failed to lookup existing signing key: %w", err))
				result = observability.ResultError("FAILED")
				return
			}
			if age, max := time.Now().UTC().Sub(existing.CreatedAt), c.config.TokenSigningKeyMaxAge; age < max {
				logger.Debugw("token signing key does not require rotation", "age", age, "max", max)
				return
			}

			// TODO(sethvargo): figure out what to do with .TokenSigningKeys since it
			// can be an array.
			key, err := c.db.RotateTokenSigningKey(ctx, c.keyManager, c.config.TokenSigning.ActiveKey(), RotationActor)
			if err != nil {
				merr = multierror.Append(merr, fmt.Errorf("failed to rotate token signing key: %w", err))
				result = observability.ResultError("FAILED")
				return
			}

			logger.Infow("rotated token signing key", "new", key)
		}()

		// If there are any errors, return them
		if merr != nil {
			if errs := merr.WrappedErrors(); len(errs) > 0 {
				logger.Errorw("failed to rotate", "errors", errs)
				c.h.RenderJSON(w, http.StatusInternalServerError, &Result{
					OK:     false,
					Errors: errs,
				})
				return
			}
		}

		c.h.RenderJSON(w, http.StatusOK, &Result{
			OK: true,
		})
	})
}

func (c *Controller) shouldRotate(ctx context.Context) (bool, error) {
	cStat, err := c.db.CreateCleanup(rotationLock)
	if err != nil {
		return false, fmt.Errorf("failed to create rotation claim: %w", err)
	}

	if cStat.NotBefore.After(time.Now().UTC()) {
		return false, nil
	}

	if _, err = c.db.ClaimCleanup(cStat, c.config.MinTTL); err != nil {
		stats.RecordWithTags(ctx, []tag.Mutator{observability.ResultNotOK()}, mClaimRequests.M(1))
		return false, fmt.Errorf("failed to claim rotation: %w", err)
	}
	stats.RecordWithTags(ctx, []tag.Mutator{observability.ResultOK()}, mClaimRequests.M(1))
	return true, nil
}