package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/customengine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/provision"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

var recipeInterfacePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
var localRecipeIDPattern = regexp.MustCompile(`^(?:deepseek-v4-flash-dspark-2x|local-[0-9a-f]{16})$`)

type recipeJobControl struct {
	cancel context.CancelFunc
	job    *jobs.Job
}

func (s *Server) localRecipesList(w http.ResponseWriter, r *http.Request) {
	recipes, err := s.recipes.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	active := ""
	if st := s.state.Get(); st.LocalRecipeID != "" && !st.EngineUnloaded {
		active = st.LocalRecipeID
	}
	plans := make(map[string]recipeLaunchPlan, len(recipes))
	for _, recipe := range recipes {
		plans[recipe.ID] = buildRecipeLaunchPlan(r.Context(), recipe)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"recipes": recipes, "active": active, "jobs": s.jobs.List("recipe:"),
		"defaults": localrecipes.NewDraft(),
		"cluster":  clusterCompute(r.Context(), totalVRAMGB(r.Context())),
		"plans":    plans,
	})
}

func (s *Server) localRecipeCreate(w http.ResponseWriter, r *http.Request) {
	var draft localrecipes.Draft
	if err := json.NewDecoder(r.Body).Decode(&draft); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "recipe details are required"})
		return
	}
	recipe, err := s.recipes.Create(draft)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, recipe)
}

func (s *Server) localRecipeUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if st := s.state.Get(); st.LocalRecipeID == id && !st.EngineUnloaded {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "stop this recipe before changing its operating profile"})
		return
	}
	var draft localrecipes.Draft
	if err := json.NewDecoder(r.Body).Decode(&draft); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "recipe details are required"})
		return
	}
	recipe, err := s.recipes.Update(id, draft)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "local recipe not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, recipe)
}

func (s *Server) localRecipeImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SourceURL string `json:"sourceUrl"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sourceUrl is required"})
		return
	}
	recipe, err := s.recipes.Import(body.SourceURL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, recipe)
}

func (s *Server) localRecipeImportPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SourceURL string `json:"sourceUrl"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sourceUrl is required"})
		return
	}
	recipe, err := s.recipes.PreviewImport(body.SourceURL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":   "cloudless",
		"recipe": recipe,
		"draft":  localrecipes.DraftFromRecipe(recipe),
	})
}

func (s *Server) localRecipeDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if st := s.state.Get(); !st.EngineUnloaded && st.LocalRecipeID == id {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "stop this recipe before removing it"})
		return
	}
	if err := s.recipes.Delete(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) localRecipeSource(w http.ResponseWriter, r *http.Request) {
	recipe, ok, err := s.recipes.Get(r.PathValue("id"))
	if err != nil {
		http.Error(w, "Could not load this recipe.", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	if recipe.Source.URL != localrecipes.DeepSeekDSparkSource || recipe.Source.Revision != localrecipes.DeepSeekDSparkRevision {
		configuration, marshalErr := json.MarshalIndent(localrecipes.DraftFromRecipe(recipe), "", "  ")
		if marshalErr != nil {
			http.Error(w, "Could not render this recipe.", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = io.WriteString(w, recipeSourceDocument(recipe, "Complete editable Cloudless recipe configuration\n\n"+string(configuration)))
		return
	}
	readmeURL := "https://raw.githubusercontent.com/tonyd2wild/DeepSeek-v4-Flash-DSpark-60-tok-s-900K-ctx-2x-DGX-Spark/" + recipe.Source.Revision + "/README.md"
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, readmeURL, nil)
	if err != nil {
		http.Error(w, "Could not prepare the reviewed source.", http.StatusInternalServerError)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "The reviewed source is temporarily unavailable.", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "The reviewed source is temporarily unavailable.", http.StatusBadGateway)
		return
	}
	readme, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		http.Error(w, "Could not read the reviewed source.", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, recipeSourceDocument(recipe, string(readme)))
}

func recipeSourceDocument(recipe localrecipes.Recipe, readme string) string {
	short := func(value string) string {
		if len(value) > 12 {
			return value[:12]
		}
		return value
	}
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(recipe.Name) + `</title><style>
:root{color-scheme:dark;font-family:Inter,ui-sans-serif,system-ui,sans-serif;background:#0d1220;color:#eef2ff}*{box-sizing:border-box}body{margin:0;padding:32px;background:radial-gradient(circle at 85% 0,rgba(95,139,255,.12),transparent 34%),#0d1220}.page{max-width:980px;margin:auto}.head{padding:22px;border:1px solid #293149;border-radius:18px;background:#141a2a}.eyebrow{color:#7f9cff;font-size:11px;font-weight:800;letter-spacing:.12em;text-transform:uppercase}h1{margin:7px 0 8px;font-size:24px}.meta{color:#9da8c2;font-size:13px;line-height:1.6}.pin{display:inline-block;margin:12px 7px 0 0;padding:6px 9px;border:1px solid #303a56;border-radius:8px;background:#1b2235;color:#c9d3ed;font:11px ui-monospace,monospace}.source{margin-top:16px;padding:22px;overflow:auto;border:1px solid #293149;border-radius:18px;background:#111726;color:#dce4f8;font:13px/1.65 ui-monospace,SFMono-Regular,Menlo,monospace;white-space:pre-wrap;overflow-wrap:anywhere}</style></head><body><main class="page"><section class="head"><div class="eyebrow">Reviewed recipe source</div><h1>` + html.EscapeString(recipe.Name) + `</h1><div class="meta">` + html.EscapeString(recipe.SourceURL) + `<br>This is the README from the exact source revision saved by Cloudless.</div><span class="pin">Recipe ` + html.EscapeString(short(recipe.Revision)) + `</span><span class="pin">Model ` + html.EscapeString(short(recipe.ModelRevision)) + `</span></section><pre class="source">` + html.EscapeString(readme) + `</pre></main></body></html>`
}

func (s *Server) localRecipeRun(w http.ResponseWriter, r *http.Request) {
	recipe, ok, err := s.recipes.Get(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "local recipe not found"})
		return
	}
	if st := s.state.Get(); st.LocalRecipeID != "" && !st.EngineUnloaded {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "stop the active local recipe before starting another one"})
		return
	}
	for _, snapshot := range s.jobs.List("recipe:") {
		if !snapshot.Done {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "another recipe operation is already running", "jobId": snapshot.ID})
			return
		}
	}
	job := s.jobs.Create("recipe:" + recipe.ID)
	go s.runLocalRecipe(job, recipe)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "recipe": recipe.ID})
}

func (s *Server) localRecipeCheck(w http.ResponseWriter, r *http.Request) {
	recipe, ok, err := s.recipes.Get(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "local recipe not found"})
		return
	}
	for _, snapshot := range s.jobs.List("recipe:") {
		if !snapshot.Done {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "another recipe operation is already running", "jobId": snapshot.ID})
			return
		}
	}
	job := s.jobs.Create("recipe:" + recipe.ID + ":check")
	go s.checkLocalRecipe(job, recipe)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "recipe": recipe.ID})
}

func (s *Server) localRecipeAbort(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.recipeJobsMu.Lock()
	control := s.recipeJobs[id]
	s.recipeJobsMu.Unlock()
	if control.cancel == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this recipe is not currently starting"})
		return
	}
	if control.job.Snapshot().Done {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this recipe operation has already finished"})
		return
	}
	control.job.Progress("stopping", "Stopping the recipe operation and its child processes...", -1, -1)
	control.cancel()
	writeJSON(w, http.StatusAccepted, map[string]string{"recipe": id})
}

func (s *Server) registerRecipeJob(id string, cancel context.CancelFunc, job *jobs.Job) {
	s.recipeJobsMu.Lock()
	defer s.recipeJobsMu.Unlock()
	if s.recipeJobs == nil {
		s.recipeJobs = make(map[string]recipeJobControl)
	}
	s.recipeJobs[id] = recipeJobControl{cancel: cancel, job: job}
}

func (s *Server) unregisterRecipeJob(id string) {
	s.recipeJobsMu.Lock()
	delete(s.recipeJobs, id)
	s.recipeJobsMu.Unlock()
}

func (s *Server) localRecipeStop(w http.ResponseWriter, r *http.Request) {
	recipe, ok, err := s.recipes.Get(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "local recipe not found"})
		return
	}
	job := s.jobs.Create("recipe:" + recipe.ID + ":stop")
	go s.stopLocalRecipe(job, recipe)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "recipe": recipe.ID})
}

func recipeCheckout(recipe localrecipes.Recipe) string {
	return filepath.Join("/var/tmp/cloudless-recipes", recipe.ID)
}

func commandEnv(extra map[string]string) []string {
	env := os.Environ()
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	return env
}

func runRecipeCommand(ctx context.Context, job *jobs.Job, phase, label, dir string, env map[string]string, name string, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = commandEnv(env)
	// Every recipe command owns a process group. Canceling only the wrapper
	// shell leaves docker/buildx and other descendants running with the output
	// pipe open, which made Abort appear to do nothing.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	reader, writer := io.Pipe()
	cmd.Stdout, cmd.Stderr = writer, writer
	if err := cmd.Start(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return err
	}
	done := make(chan error, 1)
	processDone := make(chan struct{})
	go func() {
		err := cmd.Wait()
		_ = writer.CloseWithError(err)
		done <- err
		close(processDone)
	}()
	go func() {
		select {
		case <-ctx.Done():
			// Ask the complete command tree to stop cleanly, then force it down
			// if a container client ignores termination.
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			select {
			case <-processDone:
			case <-time.After(2 * time.Second):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-processDone:
		}
	}()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	progress := newRecipeCommandProgress(phase, label)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if len(line) > 220 {
			line = line[:217] + "..."
		}
		if message, doneBytes, totalBytes, ok := progress.parse(line); ok {
			job.ProgressBytes(phase, message, doneBytes, totalBytes)
		} else {
			job.Progress(phase, label+": "+line, -1, -1)
		}
	}
	err := <-done
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return scanner.Err()
}

func verifyRecipeFiles(recipe localrecipes.Recipe, checkout string) error {
	for name, expected := range recipe.Source.Files {
		data, err := os.ReadFile(filepath.Join(checkout, name))
		if err != nil {
			return fmt.Errorf("verify %s: %w", name, err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != expected {
			return fmt.Errorf("reviewed file changed: %s", name)
		}
	}
	return nil
}

// prepareRecipeCompose makes one explicit Cloudless-managed change after the
// reviewed source hashes have been verified: recipe containers restart after a
// host reboot. The exact match makes an upstream layout change fail closed.
func prepareRecipeCompose(checkout, restartPolicy string) error {
	path := filepath.Join(checkout, "docker-compose.dspark.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	needle := "    image: ${DSPARK_VLLM_IMAGE:-vllm-dspark-runtime:clean}\n"
	if strings.Count(string(data), needle) != 1 {
		return errors.New("reviewed compose service layout changed")
	}
	managed := strings.Replace(string(data), needle, needle+"    restart: "+restartPolicy+"\n", 1)
	return os.WriteFile(path, []byte(managed), 0o600)
}

// prepareRecipeModelPin teaches the reviewed download helper to use the exact
// model snapshot stored in the local recipe. Each substitution is exact and
// counted so upstream changes fail instead of weakening reproducibility.
func prepareRecipeModelPin(recipe localrecipes.Recipe, checkout string) error {
	path := filepath.Join(checkout, "prepare-dspark-model-cache.sh")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	managed := string(data)
	replacements := []struct {
		old, new string
		count    int
	}{
		{"    -e DSPARK_MODEL=\"$DSPARK_MODEL\" \\\n", "    -e DSPARK_MODEL=\"$DSPARK_MODEL\" \\\n    -e DSPARK_MODEL_REVISION=\"$DSPARK_MODEL_REVISION\" \\\n", 2},
		{"snapshot_download(os.environ[\"DSPARK_MODEL\"], max_workers=", "snapshot_download(os.environ[\"DSPARK_MODEL\"], revision=os.environ[\"DSPARK_MODEL_REVISION\"], max_workers=", 1},
		{"snapshot_download(os.environ[\"DSPARK_MODEL\"], local_files_only=True)", "snapshot_download(os.environ[\"DSPARK_MODEL\"], revision=os.environ[\"DSPARK_MODEL_REVISION\"], local_files_only=True)", 1},
	}
	for _, replacement := range replacements {
		if strings.Count(managed, replacement.old) != replacement.count {
			return errors.New("reviewed model download helper layout changed")
		}
		managed = strings.Replace(managed, replacement.old, replacement.new, replacement.count)
	}
	return os.WriteFile(path, []byte(managed), 0o700)
}

func prepareRecipeCheckout(ctx context.Context, job *jobs.Job, recipe localrecipes.Recipe) (string, error) {
	checkout := recipeCheckout(recipe)
	if !localRecipeIDPattern.MatchString(recipe.ID) || checkout != filepath.Join("/var/tmp/cloudless-recipes", recipe.ID) {
		return "", errors.New("unsafe recipe working directory")
	}
	if err := os.RemoveAll(checkout); err != nil {
		return "", err
	}
	if err := os.MkdirAll(checkout, 0o700); err != nil {
		return "", err
	}
	if recipe.Source.URL == "" {
		return checkout, nil
	}
	if err := runRecipeCommand(ctx, job, "source", "Preparing pinned source", checkout, nil, "git", "init", "--quiet"); err != nil {
		return "", err
	}
	if err := runRecipeCommand(ctx, job, "source", "Connecting recipe source", checkout, nil, "git", "remote", "add", "origin", strings.TrimSuffix(recipe.Source.URL, ".git")+".git"); err != nil {
		return "", err
	}
	if err := runRecipeCommand(ctx, job, "source", "Downloading selected revision", checkout, nil, "git", "fetch", "--depth", "1", "origin", recipe.Source.Revision); err != nil {
		return "", err
	}
	if err := runRecipeCommand(ctx, job, "source", "Checking out reviewed revision", checkout, nil, "git", "checkout", "--quiet", "--detach", "FETCH_HEAD"); err != nil {
		return "", err
	}
	if len(recipe.Source.Files) > 0 {
		job.Progress("source", "Verifying source file checksums...", -1, -1)
	}
	if err := verifyRecipeFiles(recipe, checkout); err != nil {
		return "", err
	}
	// Preserve the compatibility patches required by the reviewed DSpark
	// source. Other sources are executed exactly as configured by the user.
	if recipe.Source.URL == localrecipes.DeepSeekDSparkSource && recipe.Source.Revision == localrecipes.DeepSeekDSparkRevision {
		if err := prepareRecipeCompose(checkout, recipe.Engine.RestartPolicy); err != nil {
			return "", err
		}
		if err := prepareRecipeModelPin(recipe, checkout); err != nil {
			return "", err
		}
	}
	return checkout, nil
}

func detectRecipeHCA(iface string) (string, error) {
	if !recipeInterfacePattern.MatchString(iface) {
		return "", errors.New("the Spark fabric interface is invalid")
	}
	entries, err := os.ReadDir(filepath.Join("/sys/class/net", iface, "device", "infiniband"))
	if err != nil || len(entries) == 0 {
		return "", fmt.Errorf("no RoCE adapter was found for %s", iface)
	}
	return entries[0].Name(), nil
}

func writeRecipeRuntime(recipe localrecipes.Recipe, checkout string, cluster sparkcluster.State, requireHealthy bool) (map[string]string, string, error) {
	nodes := recipe.Distributed.Nodes
	if nodes > 1 && (cluster.NodeCount != nodes || len(cluster.Nodes) != nodes-1 || (requireHealthy && (!cluster.Healthy || !cluster.WorkerReady))) {
		return nil, "", fmt.Errorf("this recipe requires exactly %d healthy nodes", nodes)
	}
	if nodes > 1 && (len(cluster.LocalIPs) == 0 || len(cluster.LocalLinks) == 0) {
		return nil, "", errors.New("the cluster is missing its local fabric address")
	}
	iface := recipe.Distributed.Interface
	if iface == "" || iface == "auto" {
		if len(cluster.LocalLinks) > 0 {
			iface = cluster.LocalLinks[0]
		}
	}
	if nodes > 1 && iface == "" {
		return nil, "", errors.New("no distributed network interface was selected")
	}
	if !recipeInterfacePattern.MatchString(iface) {
		if nodes > 1 {
			return nil, "", errors.New("the cluster fabric interface is invalid")
		}
	}
	hca := recipe.Distributed.HCA
	if nodes > 1 && requireHealthy && (hca == "" || hca == "auto") {
		var err error
		hca, err = detectRecipeHCA(iface)
		if err != nil {
			return nil, "", err
		}
	}
	key, known := sparkcluster.SSHIdentityPaths()
	home := filepath.Join(checkout, ".cloudless-home")
	sshDir, binDir := filepath.Join(home, ".ssh"), filepath.Join(home, "bin")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return nil, "", err
	}
	aliases := make([]string, 0, len(cluster.Nodes))
	var config strings.Builder
	for index, node := range cluster.Nodes {
		alias := recipe.Distributed.WorkerAlias
		if index > 0 {
			alias += "-" + strconv.Itoa(index+1)
		}
		aliases = append(aliases, alias)
		// Recipe artifacts are large. Once the fabric is configured, carry SSH,
		// image and model-cache traffic over ConnectX instead of management
		// Wi-Fi/Ethernet. HostKeyAlias preserves the key pinned during enrollment.
		host := node.Host
		hostKeyAlias := ""
		if len(node.IPs) > 0 && net.ParseIP(node.IPs[0]) != nil {
			host = node.IPs[0]
			hostKeyAlias = node.Host
		}
		fmt.Fprintf(&config, "Host %s\n  HostName %s\n  User %s\n", alias, host, node.Username)
		if hostKeyAlias != "" {
			fmt.Fprintf(&config, "  HostKeyAlias %s\n", hostKeyAlias)
		}
		fmt.Fprintf(&config, "  IdentityFile %s\n  UserKnownHostsFile %s\n  IdentitiesOnly yes\n  BatchMode yes\n  StrictHostKeyChecking yes\n", key, known)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(config.String()), 0o600); err != nil {
		return nil, "", err
	}
	wrappers := map[string]string{
		"ssh": "#!/bin/sh\nexec /usr/bin/ssh -F " + filepath.Join(sshDir, "config") + " \"$@\"\n",
		"scp": "#!/bin/sh\nexec /usr/bin/scp -F " + filepath.Join(sshDir, "config") + " \"$@\"\n",
	}
	for name, content := range wrappers {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(content), 0o700); err != nil {
			return nil, "", err
		}
	}
	masterAddress := "127.0.0.1"
	if len(cluster.LocalIPs) > 0 {
		masterAddress = cluster.LocalIPs[0]
	}
	values := map[string]string{
		"WORKER_HOST": strings.Join(aliases, ","), "WORKER_HOSTS": strings.Join(aliases, ","),
		"MASTER_ADDR":        masterAddress,
		"MASTER_PORT":        strconv.Itoa(recipe.Distributed.MasterPort),
		"NCCL_IB_HCA":        hca,
		"NCCL_SOCKET_IFNAME": iface, "NCCL_IB_GID_INDEX": strconv.Itoa(recipe.Distributed.IBGIDIndex),
		"DISTRIBUTED_BACKEND": recipe.Distributed.Backend, "NODE_COUNT": strconv.Itoa(nodes),
		"DSPARK_MODEL": recipe.Model.ID, "DSPARK_MODEL_REVISION": recipe.Model.Revision,
		"SERVED_MODEL_NAME": recipe.Engine.ServedModelName, "DSPARK_VLLM_IMAGE": recipe.Engine.Image,
		"ENGINE_TYPE": recipe.Engine.Type, "ENGINE_IMAGE": recipe.Engine.Image,
		"ENGINE_PORT": strconv.Itoa(recipe.Engine.ContainerPort), "ENGINE_API_PATH": recipe.Engine.APIPath,
		"MAX_MODEL_LEN": strconv.Itoa(recipe.Model.MaxContext), "MAX_NUM_SEQS": strconv.Itoa(recipe.Model.MaxSequences),
		"GPU_MEMORY_UTILIZATION": strconv.FormatFloat(recipe.Model.GPUMemoryUtilization, 'f', -1, 64),
		"TENSOR_PARALLEL_SIZE":   strconv.Itoa(recipe.Model.TensorParallel), "PIPELINE_PARALLEL_SIZE": strconv.Itoa(recipe.Model.PipelineParallel),
		"QUANTIZATION": recipe.Model.Quantization, "DTYPE": recipe.Model.DType, "KV_CACHE_DTYPE": recipe.Model.KVCacheDType,
		"TRUST_REMOTE_CODE": strconv.FormatBool(recipe.Model.TrustRemoteCode), "ENGINE_ARGS": strings.Join(recipe.Engine.Arguments, " "),
	}
	for key, value := range recipe.Runtime.Environment {
		values[key] = value
	}
	if recipe.Runtime.BuildOnce && nodes > 1 {
		values["WORKER_BUILD"] = "0"
	}
	if recipe.Runtime.DownloadOnce && nodes > 1 {
		values["PREPARE_WORKER"] = "0"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var envText strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&envText, "%s=%s\n", key, values[key])
	}
	if err := os.WriteFile(filepath.Join(checkout, ".env.dspark"), []byte(envText.String()), 0o600); err != nil {
		return nil, "", err
	}
	workdir := filepath.Clean(filepath.Join(checkout, recipe.Runtime.WorkingDir))
	if workdir != checkout && !strings.HasPrefix(workdir, checkout+string(os.PathSeparator)) {
		return nil, "", errors.New("runtime working directory escapes the recipe checkout")
	}
	if err := os.MkdirAll(workdir, 0o700); err != nil {
		return nil, "", err
	}
	commandEnvironment := map[string]string{
		"HOME": home, "PATH": binDir + ":" + os.Getenv("PATH"),
		"RSYNC_RSH":       "/usr/bin/ssh -F " + filepath.Join(sshDir, "config"),
		"ENV_FILE":        filepath.Join(checkout, ".env.dspark"),
		"WORKER_CHECKOUT": checkout,
	}
	for key, value := range values {
		commandEnvironment[key] = value
	}
	return commandEnvironment, workdir, nil
}

func (s *Server) stopManagedEngines(ctx context.Context) error {
	_ = sparkcluster.StopWorker(ctx)
	_ = s.eng.Remove(ctx, "cloudless-cluster-engine-proxy")
	for _, candidate := range customengine.All(s.state) {
		if err := s.eng.Remove(ctx, candidate.ContainerName()); err != nil {
			if found, _ := s.eng.Find(ctx, candidate.ContainerName()); found != nil {
				return err
			}
		}
	}
	return nil
}

func runConfiguredRecipeCommand(ctx context.Context, job *jobs.Job, phase, label, dir string, env map[string]string, command localrecipes.Command) error {
	if command.Program == "" {
		return nil
	}
	return runRecipeCommand(ctx, job, phase, label, dir, env, command.Program, command.Args...)
}

// stopActiveLocalRecipeRuntime is called while EngineMu is held. It makes a
// recipe participate in the normal unload/switch lifecycle even when it is an
// imported compatibility recipe rather than a catalog engine.
func (s *Server) stopActiveLocalRecipeRuntime(parent context.Context, job *jobs.Job) {
	st := s.state.Get()
	if st.LocalRecipeID == "" || st.EngineUnloaded {
		return
	}
	recipe, ok, err := s.recipes.Get(st.LocalRecipeID)
	if err != nil || !ok {
		_ = s.state.SetLocalRecipe("")
		return
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	job.Progress("stopping", "Stopping the active recipe runtime...", -1, -1)
	checkout := recipeCheckout(recipe)
	cluster, _ := sparkcluster.Status(ctx)
	if env, workdir, runtimeErr := writeRecipeRuntime(recipe, checkout, cluster, false); runtimeErr == nil {
		_ = runConfiguredRecipeCommand(ctx, job, "stopping", "Stop inference", workdir, env, recipe.Runtime.Lifecycle.Stop)
	}
	_ = s.eng.Remove(ctx, "cloudless-cluster-engine-proxy")
	_ = s.state.SetLocalRecipe("")
}

func waitRecipeHealth(ctx context.Context, job *jobs.Job, recipe localrecipes.Recipe) error {
	address := fmt.Sprintf("%s://%s:%d%s", recipe.Health.Scheme, recipe.Health.Host, recipe.Health.Port, recipe.Health.Path)
	deadline := time.Now().Add(time.Duration(recipe.Health.TimeoutSeconds) * time.Second)
	client := &http.Client{Timeout: 5 * time.Second}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err == nil {
			if response, requestErr := client.Do(req); requestErr == nil {
				_ = response.Body.Close()
				if response.StatusCode >= 200 && response.StatusCode < 500 {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("engine health check did not become ready at %s", address)
		}
		job.Progress("health", "Waiting for engine health check at "+address, -1, -1)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(recipe.Health.IntervalSeconds) * time.Second):
		}
	}
}

func (s *Server) runLocalRecipe(job *jobs.Job, recipe localrecipes.Recipe) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(recipe.Runtime.TimeoutMinutes)*time.Minute)
	defer cancel()
	s.registerRecipeJob(recipe.ID, cancel, job)
	defer s.unregisterRecipeJob(recipe.ID)
	provision.EngineMu.Lock()
	defer provision.EngineMu.Unlock()
	for _, command := range recipe.Runtime.Prerequisites {
		if _, err := exec.LookPath(command); err != nil {
			job.Fail(fmt.Errorf("recipe prerequisite %q is not installed", command))
			return
		}
	}
	cluster, err := sparkcluster.Status(ctx)
	if err != nil && recipe.Distributed.Nodes > 1 {
		finishRecipeJob(job, err)
		return
	}
	totalSteps := 8
	if recipe.Distributed.Nodes > 1 && recipe.Runtime.BuildOnce {
		totalSteps++
	}
	if recipe.Distributed.Nodes > 1 && recipe.Runtime.DownloadOnce {
		totalSteps++
	}
	step := 0
	job.Progress("source", "Preparing the recipe source and runtime...", step, totalSteps)
	checkout, err := prepareRecipeCheckout(ctx, job, recipe)
	if err != nil {
		finishRecipeJob(job, err)
		return
	}
	env, workdir, err := writeRecipeRuntime(recipe, checkout, cluster, true)
	if err != nil {
		finishRecipeJob(job, err)
		return
	}
	var peers []recipePeer
	if recipe.Distributed.Nodes > 1 && (recipe.Runtime.BuildOnce || recipe.Runtime.DownloadOnce) {
		peers, err = recipeDistributionPeers(recipe, cluster, env)
		if err != nil {
			finishRecipeJob(job, err)
			return
		}
	}
	step++
	job.Progress("building", "Building the inference runtime once on this Spark...", step, totalSteps)
	if err := runConfiguredRecipeCommand(ctx, job, "building", "Build", workdir, env, recipe.Runtime.Lifecycle.Build); err != nil {
		finishRecipeJob(job, err)
		return
	}
	step++
	if recipe.Distributed.Nodes > 1 && recipe.Runtime.BuildOnce {
		job.Progress("syncing-image", "Preparing to send the completed runtime over the Spark fabric...", step, totalSteps)
		if err := syncRecipeCheckout(ctx, job, checkout, env, peers); err != nil {
			finishRecipeJob(job, err)
			return
		}
		if err := distributeRecipeImage(ctx, job, recipe, workdir, env, peers); err != nil {
			finishRecipeJob(job, err)
			return
		}
		step++
	}
	job.ProgressBytes("downloading", "Downloading the model once on this Spark...", 0, 0)
	job.Progress("downloading", "Downloading and verifying the model once on this Spark...", step, totalSteps)
	if err := runConfiguredRecipeCommand(ctx, job, "downloading", "Model preparation", workdir, env, recipe.Runtime.Lifecycle.Download); err != nil {
		finishRecipeJob(job, err)
		return
	}
	step++
	if recipe.Distributed.Nodes > 1 && recipe.Runtime.DownloadOnce {
		job.Progress("syncing-model", "Preparing to send the model over the Spark fabric...", step, totalSteps)
		if err := distributeRecipeModel(ctx, job, recipe, workdir, env, peers); err != nil {
			finishRecipeJob(job, err)
			return
		}
		step++
	}
	// Keep the current model available during the long image build and model
	// download. Only release it when the reviewed runtime is ready to start.
	job.ProgressBytes("stopping", "Switching from the current Cloudless model...", 0, 0)
	job.Progress("stopping", "Switching from the current Cloudless model...", step, totalSteps)
	if err := s.stopManagedEngines(ctx); err != nil {
		finishRecipeJob(job, err)
		return
	}
	_ = s.state.SetEngineUnloaded(true)
	step++
	job.Progress("starting", "Starting the inference recipe...", step, totalSteps)
	startErr := runConfiguredRecipeCommand(ctx, job, "starting", "Start inference", workdir, env, recipe.Runtime.Lifecycle.Start)
	if startErr != nil {
		finishRecipeJob(job, startErr)
		return
	}
	step++
	job.Progress("health", "Checking that the configured engine is ready...", step, totalSteps)
	if err := waitRecipeHealth(ctx, job, recipe); err != nil {
		finishRecipeJob(job, err)
		return
	}
	step++
	job.Progress("connecting", "Connecting Cloudless apps to the recipe runtime...", step, totalSteps)
	proxyImage := s.infraImage(ctx, "socat", catalog.SocatImage)
	if err := s.eng.Pull(ctx, proxyImage); err != nil {
		finishRecipeJob(job, err)
		return
	}
	spec := sparkcluster.ProxySpecTarget(recipe.Engine.ProxyHost, recipe.Engine.ContainerPort)
	spec.Image = proxyImage
	if _, err := s.eng.Run(ctx, spec); err != nil {
		finishRecipeJob(job, err)
		return
	}
	if err := s.state.SetModel(recipe.Model.ID); err != nil {
		finishRecipeJob(job, err)
		return
	}
	_ = s.state.SetEngine(recipe.Engine.Type)
	mode := "local"
	if recipe.Distributed.Nodes > 1 {
		mode = "cluster"
	}
	_ = s.state.SetExecutionMode(mode)
	_ = s.state.SetLocalRecipe(recipe.ID)
	_ = s.state.SetEngineUnloaded(false)
	job.Progress("ready", "Recipe is running through the normal Cloudless API.", totalSteps, totalSteps)
	job.Succeed(recipe.Engine.ServedModelName)
}

func finishRecipeJob(job *jobs.Job, err error) {
	if errors.Is(err, context.Canceled) {
		job.Cancel()
		return
	}
	job.Fail(err)
}

func (s *Server) checkLocalRecipe(job *jobs.Job, recipe localrecipes.Recipe) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	job.Progress("checking-cluster", "Reading the connected cluster topology and accelerator availability...", 0, 4)
	cluster, err := sparkcluster.Status(ctx)
	if err != nil && recipe.Distributed.Nodes > 1 {
		job.Fail(err)
		return
	}
	job.Progress("checking-topology", "Comparing the recipe's node requirements with this cluster...", 1, 4)
	if _, _, err := writeRecipeRuntime(recipe, recipeCheckout(recipe)+"-check", cluster, true); err != nil {
		job.Fail(err)
		return
	}
	job.Progress("checking-tools", "Checking required commands without launching the model...", 2, 4)
	for _, command := range recipe.Runtime.Prerequisites {
		if _, err := exec.LookPath(command); err != nil {
			job.Fail(fmt.Errorf("recipe prerequisite %q is not installed", command))
			return
		}
	}
	job.Progress("checking-source", "Validating the pinned source and runtime configuration...", 3, 4)
	if recipe.Source.URL != "" && recipe.Source.Revision == "" {
		job.Fail(errors.New("a pinned source revision is required"))
		return
	}
	job.Progress("validated", "Cloudless validated this recipe without launching it.", 4, 4)
	job.Succeed("validated")
}

func (s *Server) stopLocalRecipe(job *jobs.Job, recipe localrecipes.Recipe) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	provision.EngineMu.Lock()
	defer provision.EngineMu.Unlock()
	checkout := recipeCheckout(recipe)
	job.Progress("stopping", "Running the configured stop step...", 0, 1)
	cluster, err := sparkcluster.Status(ctx)
	if err == nil || recipe.Distributed.Nodes == 1 {
		if env, workdir, envErr := writeRecipeRuntime(recipe, checkout, cluster, false); envErr == nil {
			_ = runConfiguredRecipeCommand(ctx, job, "stopping", "Stop inference", workdir, env, recipe.Runtime.Lifecycle.Stop)
		}
	}
	_ = s.eng.Remove(ctx, "cloudless-cluster-engine-proxy")
	_ = s.state.SetLocalRecipe("")
	if err := s.state.SetEngineUnloaded(true); err != nil {
		job.Fail(err)
		return
	}
	job.Progress("stopped", "Recipe stopped. The downloaded model remains cached.", 1, 1)
	job.Succeed("")
}
