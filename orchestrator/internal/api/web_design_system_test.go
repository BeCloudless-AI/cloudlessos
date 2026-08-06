package api

import (
	"strings"
	"testing"
)

func TestEmbeddedWebDefinesSharedControlContract(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	required := []string{
		"--control-height: 36px",
		"--control-gap: 8px",
		"--radius-control: 10px",
		"--window-settings-width: 1180px",
		"--window-primary-width: 1280px",
		":where(button, a, input, select, textarea):focus-visible",
		"display: inline-flex; align-items: center; justify-content: center; gap: var(--control-gap)",
		"line-height: 1; white-space: nowrap",
		".btn > .ui-icon { width: 15px; height: 15px; flex: 0 0 15px",
	}
	for _, want := range required {
		if !strings.Contains(web, want) {
			t.Fatalf("embedded frontend is missing shared design-system contract %q", want)
		}
	}
}

func TestEmbeddedWebPrimaryWindowsUseSharedGeometry(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	required := []string{
		"width: min(var(--window-settings-width), calc(var(--ui-vw, 100vw) - 118px))",
		"height: min(var(--window-settings-height), calc(var(--ui-vh, 100vh) - 100px))",
		"width: min(var(--window-primary-width), var(--ui-vw-96, 96vw))",
		"height: min(var(--ui-vh-92, 92vh), var(--window-primary-height))",
	}
	for _, want := range required {
		if !strings.Contains(web, want) {
			t.Fatalf("embedded frontend is missing shared window geometry %q", want)
		}
	}
}

func TestEmbeddedWebRecipeActionsKeepDestructiveControlReadable(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	for _, want := range []string{
		".recipe-actions { width: 100%; display: grid; gap: 8px; }",
		".recipe-secondary-actions { display: grid; grid-template-columns: repeat(auto-fit,minmax(76px,1fr)); gap: 7px; }",
		".recipe-actions .recipe-primary-action { grid-column: 1 / -1;",
	} {
		if !strings.Contains(web, want) {
			t.Fatalf("embedded recipe action layout is missing %q", want)
		}
	}
}

func TestEmbeddedWebExplainsRecipeRuntimeImageProgress(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	for _, want := range []string{
		"'pulling-image':'building'",
		"'exporting-image':'syncing-image'",
		"'pulling-image':'Downloading recipe runtime'",
		"'exporting-image':'Packaging runtime for the cluster'",
		"Container registry → primary Spark",
		"Primary Spark local storage",
		"job.phase==='downloading'||job.phase==='pulling-image'",
	} {
		if !strings.Contains(web, want) {
			t.Fatalf("embedded recipe image progress is missing %q", want)
		}
	}
}

func TestEmbeddedWebAppSurfacesFollowRuntimeLifecycle(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	required := []string{
		`.filter(([, state]) => state === 'running')`,
		`catalog.filter(a => a.id !== 'hermes' && !a.service && !a.hidden && installed.has(a.id))`,
		`if (isRun || pinned) acts +=`,
		"${installed && launchApp ? `<button class=\"ac-btn pin",
	}
	for _, want := range required {
		if !strings.Contains(web, want) {
			t.Fatalf("embedded frontend does not enforce app lifecycle contract %q", want)
		}
	}
}

func TestEmbeddedWebSettingsNavigationIsImmediateAndRaceSafe(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	for _, want := range []string{
		"function renderSettings()",
		"host.replaceChildren(c)",
		"c.className = 'settings-page-mount'",
		"if (!c.isConnected) return;",
		"engineStateCache ? Promise.resolve(engineStateCache)",
	} {
		if !strings.Contains(web, want) {
			t.Fatalf("embedded Settings navigation is missing %q", want)
		}
	}
	if strings.Contains(web, "async function renderSettings()") {
		t.Fatal("Settings navigation waits for network requests before changing tabs")
	}
}

func TestEmbeddedWebMetricsAvoidGPUBackedCanvas(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	for _, want := range []string{
		`<svg class="inf-hero-canvas" id="c-tput"`,
		`<svg class="im-ring" id="c-gmem"`,
		`function infGauge(id, pct, rgb)`,
		`svg.innerHTML = markup.join('')`,
	} {
		if !strings.Contains(web, want) {
			t.Fatalf("embedded metrics view is missing low-memory SVG contract %q", want)
		}
	}
	if strings.Contains(web, `<canvas class="inf-`) || strings.Contains(web, `<canvas class="im-`) {
		t.Fatal("metrics view must not allocate GPU-backed canvas surfaces")
	}
}

func TestEmbeddedWebAccountClientOwnsTopBarIdentity(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	for _, want := range []string{
		`id="auth-control"`,
		`<span class="auth-control-name">Sign in</span>`,
		`id="auth-menu-profile"`,
		`<span>Profile</span>`,
		`id="auth-menu-keys"`,
		`<span>API Keys</span>`,
		`id="auth-menu-recipes"`,
		`<span>My Recipes</span>`,
		`id="auth-menu-logout"`,
		`<span>Log out</span>`,
		`.auth-menu-wrap:hover .auth-menu:not(.hidden)`,
		`function openPublisherKeysDialog()`,
		`function openMyRecipes()`,
		`id="my-recipes"`,
		`id="my-recipes-body"`,
		`id="my-recipes-x"`,
		`function closeMyRecipes()`,
		`renderCommunityRecipes(body,'mine')`,
		`mmRecipeView.scope='mine'`,
		`function openAuthDialog(mode = 'signin')`,
		`data-auth-mode="signup"`,
		`data-auth-provider="google"`,
		`data-auth-provider="x"`,
		`data-auth-provider="github"`,
		`function completeAuthSession(payload, message)`,
		`fetch('/api/account' + path`,
		`await authRequest('/login'`,
		`await authRequest('/signup'`,
		`await authRequest('/refresh'`,
		`await authRequest('/logout'`,
		`await authenticatedAuthRequest('/picture'`,
		`location.origin + '/auth/callback'`,
		`function consumeAuthCallback()`,
		`localStorage.setItem(AUTH_SESSION_KEY, JSON.stringify(value))`,
		`localStorage.removeItem(AUTH_SESSION_KEY)`,
		`window.open('', 'cloudless-account-provider'`,
		`providerWindow.location.replace(result.url)`,
		`window.opener.postMessage({ type: 'cloudless-account-signed-in' }, location.origin)`,
		`event.origin !== location.origin`,
		`The provider signed you in, but Cloudless could not load your account:`,
	} {
		if !strings.Contains(web, want) {
			t.Fatalf("embedded account client is missing %q", want)
		}
	}
	if strings.Contains(web, `localStorage.setItem(AUTH_SESSION_KEY, secret)`) || strings.Contains(web, `password: password.value`) {
		t.Fatal("account client must never persist the submitted password")
	}
	if strings.Contains(web, `tab('mine','user','My recipes')`) {
		t.Fatal("My Recipes must be a standalone account modal, not a Model Manager recipe tab")
	}
}

func TestEmbeddedWebModelManagerPrefersRecipesAndExplainsRawModels(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	for _, want := range []string{
		`let mmTab = 'recipes'`,
		`function openModelManager(tab = 'recipes')`,
		`<span>Recipes</span></button>`,
		`mm-tabs mm-primary-tabs`,
		`<span>Models</span></button>`,
		`data-model-kind="language"`,
		`data-model-kind="image"`,
		`Running a model without a recipe might not give you the full experience.`,
		`if (advancedInterfaceEnabled()) return '';`,
		`async function confirmModelsWithoutRecipe(returnFocus)`,
		`title: 'Continue without a recipe?'`,
		`confirmLabel: 'Continue without a recipe'`,
		`cancelLabel: 'Stay with recipes'`,
		`if (targetSection === 'models' && !(await confirmModelsWithoutRecipe(b))) return;`,
		`if (!skipConfirmation && !(await confirmModelsWithoutRecipe(document.activeElement))) return;`,
		`openGuide('models-recipes')`,
		`if (action === 'models') openModelManager('language');`,
		`.recipe-scope-tabs { width: 100%;`,
		`grid-template-columns: repeat(2,minmax(0,1fr))`,
		`.recipe-scope-tab { width: 100%;`,
		`.recipe-intro + .recipe-controls, .recipe-intro + .recipe-filter-row { border-top: 0; border-radius: 0 0 16px 16px; }`,
	} {
		if !strings.Contains(web, want) {
			t.Fatalf("embedded Model Manager recipe-first flow is missing %q", want)
		}
	}
	recipes := strings.Index(web, `data-tab="recipes"`)
	models := strings.Index(web, `data-tab="language"`)
	if recipes < 0 || models < 0 || recipes > models {
		t.Fatal("Recipes must be rendered before Models in the primary navigation")
	}
}

func TestEmbeddedWebAccountUsesDisplayNameForVisibleIdentity(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	for _, want := range []string{
		`function authDisplayName(user)`,
		`[user?.displayName, user?.username, user?.email]`,
		`if (normalized) return normalized;`,
		`const displayName = authDisplayName(user);`,
		`escapeHtml(displayName)`,
		`button.setAttribute('aria-label', ` + "`Account: ${displayName}`" + `)`,
	} {
		if !strings.Contains(web, want) {
			t.Fatalf("embedded account client does not prefer the profile display name: missing %q", want)
		}
	}
}

func TestEmbeddedWebAccountDialogsKeepActionsContained(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	for _, want := range []string{
		`class="auth-account-actions"`,
		`class="auth-account-action"`,
		`class="auth-account-footer"`,
		`class="publisher-key-list set-hint"`,
		`class="publisher-key-fields"`,
		`class="key-dialog-actions publisher-key-actions"`,
		`class="publisher-secret"`,
		`let authRefreshInFlight = null`,
		`async function refreshAuthSession()`,
		`async function authenticatedAuthRequest(path, options = {}, retried = false)`,
		`function authReconnectView(content, message = '')`,
		`id="auth-reconnect-back"`,
		`id="auth-reconnect-signin"`,
	} {
		if !strings.Contains(web, want) {
			t.Fatalf("embedded account dialog is missing contained layout contract %q", want)
		}
	}
	if strings.Contains(web, `document.getElementById('auth-signout').before(publisherButton)`) {
		t.Fatal("account actions must be rendered structurally, not injected into a fixed-width footer")
	}
	if strings.Contains(web, `authRequest('/publisher-keys',{},authSession.session.access_token)`) {
		t.Fatal("publisher keys must use the refresh-and-retry authenticated account client")
	}
}

func TestEmbeddedWebRecipeManagerDoesNotExposeAuthoringForms(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	for _, forbidden := range []string{
		`id="recipe-create"`,
		`id="recipe-community-create"`,
		`data-recipe-edit=`,
		`data-recipe-publish=`,
		`<span>Create locally</span>`,
		`<span>New recipe</span>`,
		`<span>Import and edit</span>`,
	} {
		if strings.Contains(web, forbidden) {
			t.Fatalf("recipe authoring must remain a CLI workflow; found GUI affordance %q", forbidden)
		}
	}
	for _, want := range []string{
		`Run recipes installed on this machine. Find signed community recipes in Discover.`,
		`<span>Import recipe</span>`,
		`const importAction = advanced ?`,
		`Use the Cloudless CLI to validate and publish a recipe from your development environment.`,
	} {
		if !strings.Contains(web, want) {
			t.Fatalf("recipe manager is missing the CLI-first workflow copy %q", want)
		}
	}
}

func TestEmbeddedWebCommunityRecipesInstallAndShowDetailsInline(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	for _, want := range []string{
		`data-community-install=`,
		`data-community-open-installed=`,
		`function installCommunityRecipe(`,
		`function renderCommunityRecipeDetail(`,
		`async function openRuntimeManager(runtime, details = false)`,
		`const installed = (local?.recipes || []).find(recipe => recipe.id === runtime.id)`,
		`mmCommunityDetail = installed.community.slug`,
		`data-recipe-details=`,
		`mmCommunityDetail = recipe.community.slug`,
		`Expanded local recipe configuration and validation details`,
		`class="community-detail"`,
		`class="community-detail-title"`,
		`function communityRatingHTML(`,
		`function wireCommunityRating(`,
		`function communityMutationKey(`,
		`'Idempotency-Key':communityMutationKey()`,
		`activity.viewerRating=Number(viewerRatingPayload.rating||0)`,
		`data-saved-rating="'+saved+'"`,
		`result.activity?.averageRating`,
		`data-community-rating-count`,
		`function communityHeaderActionsHTML(`,
		`function communityCommentsHTML(`,
		`class="community-comment-rating"`,
		`item.author_rating`,
		`community-detail-title .community-social-actions`,
		`aria-label="Rate this recipe"`,
		`Rated '+rating+' out of 5`,
		`id="community-install"`,
		`Follow its preparation steps, then choose Run recipe.`,
		`.community-report.hidden { display:none; }`,
		`mmRecipeView.scope==='local'&&!mmCommunityDetail`,
	} {
		if !strings.Contains(web, want) {
			t.Fatalf("community recipe workflow is missing %q", want)
		}
	}
	if strings.Contains(web, `function openCommunityRecipeDetail(slug){keyDialogReturnFocus=`) {
		t.Fatal("community recipe details must render inside the Model Manager, not in a nested dialog")
	}
	for _, obsolete := range []string{`id="community-star"`, `id="community-rating"`, `Star recipe`, `Choose a rating`} {
		if strings.Contains(web, obsolete) {
			t.Fatalf("community recipe details still contain obsolete rating control %q", obsolete)
		}
	}
	if strings.Contains(web, `<div class="community-activity"`) {
		t.Fatal("community activity must be summarized in the recipe header, not repeated in its own card")
	}
	if strings.Contains(web, `Ratings, testing notes, comments, and reporting stay with this published recipe.`) {
		t.Fatal("community comments must not repeat a redundant section heading and description")
	}
}

func TestEmbeddedWebExplainsAndConfirmsKnownRecipeVulnerabilities(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	web := string(content)
	for _, want := range []string{
		`function recipeVulnerabilityBadgeHTML(`,
		`function recipeVulnerabilityDetailsHTML(`,
		`function hydrateCommunityRecipeValidation(`,
		`Known vulnerabilities in this recipe`,
		`Local-only use lowers risk; it does not make it zero.`,
		`id="recipe-vulnerability-ack"`,
		`confirm.disabled=!ack.checked`,
		`<details class="recipe-vulnerability-panel">`,
		`if(launch)return '<div class="recipe-vulnerability-panel" role="note">`,
		`function openRecipeVulnerabilityInfo(`,
		`class="recipe-vulnerability-summary-icon"`,
		`class="btn recipe-vulnerability-explain"`,
		`<symbol id="ui-warning"`,
		`<symbol id="ui-info"`,
		`They are not vulnerabilities in the AI model.`,
		`c.querySelector('.recipe-vulnerability-explain')`,
	} {
		if !strings.Contains(web, want) {
			t.Fatalf("recipe vulnerability UX is missing %q", want)
		}
	}
	if strings.Contains(web, `risk.findings.slice(`) {
		t.Fatal("recipe vulnerability details must not silently truncate the scanner findings")
	}
}
