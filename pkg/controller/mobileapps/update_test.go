// Copyright 2020 Google LLC
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

package mobileapps_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/google/exposure-notifications-verification-server/internal/browser"
	"github.com/google/exposure-notifications-verification-server/internal/envstest"
	"github.com/google/exposure-notifications-verification-server/pkg/controller/mobileapps"
	"github.com/google/exposure-notifications-verification-server/pkg/database"
	"github.com/google/exposure-notifications-verification-server/pkg/rbac"
	"github.com/google/exposure-notifications-verification-server/pkg/render"
)

func TestHandleUpdate(t *testing.T) {
	t.Parallel()

	harness := envstest.NewServer(t, testDatabaseInstance)

	realm, user, session, err := harness.ProvisionAndLogin()
	if err != nil {
		t.Fatal(err)
	}

	cookie, err := harness.SessionCookie(session)
	if err != nil {
		t.Fatal(err)
	}

	app := &database.MobileApp{
		RealmID: realm.ID,
		Name:    "Appy",
		AppID:   "com.example.app",
		URL:     "https://app.example.com",
		OS:      database.OSTypeIOS,
	}
	if err := harness.Database.SaveMobileApp(app, database.SystemTest); err != nil {
		t.Fatal(err)
	}
	t.Run("middleware", func(t *testing.T) {
		t.Parallel()

		h, err := render.New(context.Background(), envstest.ServerAssetsPath(), true)
		if err != nil {
			t.Fatal(err)
		}

		c := mobileapps.New(harness.Cacher, harness.Database, h)
		handler := c.HandleUpdate()

		envstest.ExerciseSessionMissing(t, handler)
		envstest.ExerciseMembershipMissing(t, handler)
		envstest.ExercisePermissionMissing(t, handler)
		envstest.ExerciseIDNotFound(t, &database.Membership{
			Realm:       realm,
			User:        user,
			Permissions: rbac.MobileAppWrite,
		}, handler)
	})

	t.Run("validation", func(t *testing.T) {
		t.Parallel()

		browserCtx := browser.New(t)
		taskCtx, done := context.WithTimeout(browserCtx, 10*time.Second)
		defer done()

		u := fmt.Sprintf("http://%s/realm/mobile-apps/%d/edit", harness.Server.Addr(), app.ID)

		var nameAttrs map[string]string

		if err := chromedp.Run(taskCtx,
			browser.SetCookie(cookie),
			chromedp.Navigate(u),
			chromedp.WaitVisible(`body#mobileapps-edit`, chromedp.ByQuery),

			chromedp.SetValue(`input#name`, "", chromedp.ByQuery),
			chromedp.Click(`#submit`, chromedp.ByQuery),

			chromedp.WaitVisible(`body#mobileapps-edit`, chromedp.ByQuery),
			chromedp.Attributes(`input#name`, &nameAttrs, chromedp.ByQuery),
		); err != nil {
			t.Fatal(err)
		}

		if got, want := nameAttrs["class"], "is-invalid"; !strings.Contains(got, want) {
			t.Errorf("expected %q to contain %q", got, want)
		}
	})

	t.Run("updates", func(t *testing.T) {
		t.Parallel()

		browserCtx := browser.New(t)
		taskCtx, done := context.WithTimeout(browserCtx, 10*time.Second)
		defer done()

		u := fmt.Sprintf("http://%s/realm/mobile-apps/%d/edit", harness.Server.Addr(), app.ID)

		if err := chromedp.Run(taskCtx,
			browser.SetCookie(cookie),
			chromedp.Navigate(u),
			chromedp.WaitVisible(`body#mobileapps-edit`, chromedp.ByQuery),

			chromedp.SetValue(`input#name`, "Updated name", chromedp.ByQuery),
			chromedp.Click(`#submit`, chromedp.ByQuery),

			chromedp.WaitVisible(`body#mobileapps-show`, chromedp.ByQuery),
		); err != nil {
			t.Fatal(err)
		}

		record, err := realm.FindMobileApp(harness.Database, app.ID)
		if err != nil {
			t.Fatal(err)
		}

		if got, want := record.Name, "Updated name"; got != want {
			t.Errorf("expected %q to be %q", got, want)
		}
	})
}
