const state = {
  backendURL: "http://127.0.0.1:8787",
  ws: null,
  currentJob: "",
  tweetDetail: null,
  posts: [],
  account: null,
  trackerJob: "",
  trackerPosts: [],
  logs: [],
  scanContext: null,
  sessionBootstrapRunning: false
};

const $ = selector => document.querySelector(selector);
const $$ = selector => [...document.querySelectorAll(selector)];

document.addEventListener("DOMContentLoaded", initialize);

async function initialize() {
  const info = await window.trexDesktop.appInfo();
  state.backendURL = info.backendURL;
  setDefaultDates();
  bindUI();
  connectEvents();
  await startupCheck();
}

function bindUI() {
  $$(".tab").forEach(button => button.addEventListener("click", () => activateTab(button.dataset.tab)));
  $$("[data-close]").forEach(button => button.addEventListener("click", () => button.closest("dialog").close()));
  $("#startup-action").onclick = () => bootstrapSession(false);
  $("#scan-mode").addEventListener("change", updateScanMode);
  $("#custom-query").addEventListener("input", validateCustomQuery);
  $("#scan-button").addEventListener("click", startScan);
  $("#tracker-start").addEventListener("click", startTracker);
  $("#tracker-stop").addEventListener("click", stopTracker);
  $("#account-button").addEventListener("click", lookupAccount);
  $("#account-excel").addEventListener("click", exportCurrentAccount);
  $("#account-pdf").addEventListener("click", exportAccountPDF);
  $("#tweet-detail-button").addEventListener("click", loadTweetDetail);
  window.trexDesktop.onMenuAction(handleMenuAction);
}

async function startupCheck() {
  $("#startup-action").classList.add("hidden");
  $("#startup-action").disabled = true;
  setProgress("startup", 12, "Starting Go backend…");
  let health = false;
  for (let attempt = 0; attempt < 25; attempt++) {
    try {
      await api("/api/health");
      health = true;
      break;
    } catch {
      await wait(350);
    }
  }
  if (!health) {
    setProgress("startup", 0, "The Go backend did not start. Check the application logs or run the backend manually.");
    return;
  }
  setProgress("startup", 42, "Backend ready. Checking the X session…");
  const session = await api("/api/session/status");
  if (session.ready) {
    showProceed(session);
    return;
  }
  setProgress("startup", 55, session.message);
  await bootstrapSession(true);
}

function openWorkspace() {
  $("#startup").classList.add("hidden");
  $("#workspace").classList.remove("hidden");
}

function showProceed(session) {
  setProgress("startup", 100, "X session ready.");
  $("#session-label").textContent = session.screenName ? `@${session.screenName}` : "Session ready";
  $("#startup-action").textContent = "Proceed to Workspace";
  $("#startup-action").disabled = false;
  $("#startup-action").classList.remove("hidden");
  $("#startup-action").onclick = openWorkspace;
}

async function bootstrapSession(auto = false) {
  if (state.sessionBootstrapRunning) return;
  state.sessionBootstrapRunning = true;
  $("#startup-action").disabled = true;
  if (auto) {
    $("#startup-action").classList.add("hidden");
  }
  try {
    const response = await api("/api/session/bootstrap", { method: "POST", body: {} });
    await watchJob(response.jobId, job => setProgress("startup", job.progress, job.message));
    const session = await api("/api/session/status");
    if (!session.ready) throw new Error("Session capture completed without the required X metadata.");
    showProceed(session);
  } catch (error) {
    setProgress("startup", 0, error.message);
    $("#startup-action").textContent = "Open Microsoft Edge";
    $("#startup-action").disabled = false;
    $("#startup-action").classList.remove("hidden");
    $("#startup-action").onclick = () => bootstrapSession(false);
  } finally {
    state.sessionBootstrapRunning = false;
  }
}

function activateTab(name) {
  $$(".tab").forEach(button => button.classList.toggle("active", button.dataset.tab === name));
  $$(".tab-panel").forEach(panel => panel.classList.toggle("active", panel.id === `tab-${name}`));
}

function updateScanMode() {
  const mode = selected("scanMode");
  $("#standard-query-fields").classList.toggle("hidden", mode === "custom");
  $("#custom-query-fields").classList.toggle("hidden", mode !== "custom");
  $("#account-filter-wrap").classList.toggle("hidden", mode !== "accounts");
  $("#terms-label").textContent = mode === "accounts" ? "Account usernames" : "Keywords";
  $("#terms-input").placeholder = mode === "accounts"
    ? "narendramodi, aus_pill"
    : "Narendra Modi, Melbourne, protest";
  $("#match-mode").disabled = mode === "accounts";
}

function validateCustomQuery() {
  const query = $("#custom-query").value.trim();
  const error = queryError(query);
  const hint = $("#query-validation");
  hint.textContent = error || "Query syntax looks balanced.";
  hint.style.color = error ? "var(--danger)" : "var(--success)";
  return !error;
}

function queryError(query) {
  if (!query) return "Enter a custom query.";
  if (/[\r\n]/.test(query)) return "Custom query must stay on one line.";
  let depth = 0;
  let quoted = false;
  for (const char of query) {
    if (char === '"') quoted = !quoted;
    if (!quoted && char === "(") depth++;
    if (!quoted && char === ")") depth--;
    if (depth < 0) return "Closing bracket appears before an opening bracket.";
  }
  if (quoted) return "A quotation mark is not closed.";
  if (depth !== 0) return "Parentheses are not balanced.";
  return "";
}

async function startScan() {
  const mode = selected("scanMode");
  if (mode === "custom" && !validateCustomQuery()) return;
  const terms = commaValues($("#terms-input").value);
  if (mode !== "custom" && terms.length === 0) return showError("Enter at least one keyword or account.");
  const payload = {
    mode,
    resultMode: selected("resultMode"),
    matchMode: $("#match-mode").value,
    terms,
    accountFilters: commaValues($("#account-filter").value),
    customQuery: $("#custom-query").value.trim(),
    fromDate: $("#from-date").value,
    toDate: $("#to-date").value,
    maxPosts: 5000
  };
  state.scanContext = payload;
  state.posts = [];
  $("#posts-list").innerHTML = '<div class="empty-state">Collecting from X…</div>';
  $("#post-count").textContent = "0";
  $("#scan-button").disabled = true;
  setProgress("scan", 2, "Preparing authenticated headless X search…");
  try {
    const response = await api("/api/scan", { method: "POST", body: payload });
    state.currentJob = response.jobId;
    const result = await watchJob(response.jobId, job => {
      setProgress("scan", job.progress, job.message);
      $("#post-count").textContent = job.count;
    });
    const data = await api("/api/posts?limit=25");
    state.posts = (await api("/api/posts")).posts;
    renderPosts(data.posts, data.total);
    setProgress("scan", 100, result.message);
  } catch (error) {
    setProgress("scan", 0, error.message);
    showError(error.message);
  } finally {
    $("#scan-button").disabled = false;
  }
}

async function startTracker() {
  const terms = commaValues($("#tracker-terms").value);
  if (!terms.length) return showError("Enter at least one tracker keyword.");
  const payload = {
    request: {
      mode: "keywords",
      resultMode: "latest",
      matchMode: "OR",
      terms,
      accountFilters: [],
      customQuery: "",
      fromDate: "",
      toDate: "",
      maxPosts: 100
    },
    intervalSeconds: Number($("#tracker-interval").value) || 30
  };
  $("#tracker-start").disabled = true;
  $("#tracker-stop").disabled = false;
  $("#tracker-status").textContent = "Starting authenticated headless X tracker…";
  state.trackerPosts = [];
  try {
    const response = await api("/api/tracker/start", { method: "POST", body: payload });
    state.trackerJob = response.jobId;
    monitorTracker(response.jobId);
  } catch (error) {
    $("#tracker-status").textContent = error.message;
    $("#tracker-start").disabled = false;
    $("#tracker-stop").disabled = true;
  }
}

async function monitorTracker(jobId) {
  while (state.trackerJob === jobId) {
    try {
      const job = await api(`/api/jobs/${jobId}`);
      $("#tracker-status").textContent = job.message;
      $("#tracker-count").textContent = state.trackerPosts.length;
      if (["failed", "cancelled", "completed"].includes(job.status)) break;
    } catch (error) {
      $("#tracker-status").textContent = error.message;
      break;
    }
    await wait(800);
  }
  if (state.trackerJob === jobId) state.trackerJob = "";
  $("#tracker-start").disabled = false;
  $("#tracker-stop").disabled = true;
}

async function stopTracker() {
  if (!state.trackerJob) return;
  await api(`/api/jobs/${state.trackerJob}`, { method: "DELETE" });
  state.trackerJob = "";
  $("#tracker-status").textContent = "Tracker stopped.";
}

function renderTrackerPosts() {
  $("#tracker-count").textContent = state.trackerPosts.length;
  $("#tracker-posts").innerHTML = state.trackerPosts.slice(0, 25).map(post => `
    <article class="post-card"><div class="post-header"><span class="post-author">@${escapeHTML(post.author?.screen_name || "unknown")}</span><span class="post-time">${escapeHTML(post.createdAt || "")}</span></div><div class="post-text">${escapeHTML(post.text || "")}</div></article>
  `).join("") || '<div class="empty-state">New tracked posts will stream here.</div>';
}

function renderPosts(posts, total) {
  $("#post-count").textContent = total;
  if (!posts.length) {
    $("#posts-list").innerHTML = '<div class="empty-state">No posts were returned for this scan.</div>';
    return;
  }
  $("#posts-list").innerHTML = posts.map(post => {
    const username = post.author?.screen_name || "unknown";
    return `<article class="post-card" data-post="${escapeHTML(post.id)}">
      <div class="post-header"><span class="post-author">@${escapeHTML(username)}</span><span class="post-time">${escapeHTML(post.createdAt || "")}</span></div>
      <div class="post-text">${escapeHTML(post.text || "")}</div>
      <div class="post-actions">
        <button data-action="comments">Tweet Analysis</button>
        <button disabled title="Python ML worker is intentionally outside this milestone">ML Analysis</button>
        <button data-action="author">About Author</button>
        <button data-action="details">Show more</button>
      </div>
    </article>`;
  }).join("");
  $$(".post-card").forEach(card => card.addEventListener("click", event => handlePostAction(card.dataset.post, event)));
}

async function handlePostAction(id, event) {
  const action = event.target.dataset.action;
  if (!action) return;
  const post = state.posts.find(item => item.id === id);
  if (!post) return;
  if (action === "details") showRecordDialog("Tweet Details", post, "SEARCH RESULT");
  if (action === "author") {
    $("#account-input").value = post.author?.screen_name || "";
    activateTab("account-info");
    await lookupAccount();
  }
  if (action === "comments") {
    $("#tweet-input").value = post.url || post.id;
    activateTab("tweet-analysis");
    await loadTweetDetail();
  }
}

async function lookupAccount() {
  const screenName = cleanScreenName($("#account-input").value);
  if (!screenName) return showError("Enter an X username.");
  $("#account-button").disabled = true;
  $("#account-status").textContent = `Calling UserByScreenName and AboutAccountQuery for @${screenName}…`;
  try {
    state.account = await api("/api/account", { method: "POST", body: { screenName } });
    renderAccount(state.account);
    $("#account-status").textContent = `Loaded account intelligence for @${state.account.screenName}.`;
    $("#account-excel").disabled = false;
    $("#account-pdf").disabled = false;
  } catch (error) {
    $("#account-content").innerHTML = `<div class="empty-state">${escapeHTML(error.message)}</div>`;
    $("#account-status").textContent = "Account lookup failed.";
  } finally {
    $("#account-button").disabled = false;
  }
}

function renderAccount(account) {
  const sections = (account.sections || []).map(section => sectionHTML(section)).join("");
  $("#account-content").innerHTML = `
    <div class="account-header">
      ${account.avatarUrl ? `<img class="account-avatar" src="${escapeAttribute(account.avatarUrl)}">` : '<div class="account-avatar"></div>'}
      <div><p class="eyebrow">USERBYSCREENNAME + ABOUTACCOUNTQUERY</p><h2>${escapeHTML(account.name || "Unknown")}</h2><p>@${escapeHTML(account.screenName)}</p></div>
    </div>
    <div class="details-grid">${sections}</div>`;
}

function sectionHTML(section) {
  return `<section class="details-section"><h3>${escapeHTML(section.title)}</h3>${(section.rows || []).map(row =>
    `<div class="field-row"><span class="field-label">${escapeHTML(row.label)}</span><span class="field-value">${formatValue(row.value)}</span></div>`
  ).join("")}</section>`;
}

async function loadTweetDetail() {
  const tweet = $("#tweet-input").value.trim();
  if (!tweet) return showError("Enter a tweet URL or ID.");
  $("#tweet-detail-button").disabled = true;
  setProgress("reply", 12, "Calling TweetDetail for the focal tweet...");
  try {
    state.tweetDetail = await api("/api/tweet/detail", {
      method: "POST",
      body: { tweet }
    });
    renderTweetDetail(state.tweetDetail);
    setProgress("reply", 100, "Tweet details loaded.");
  } catch (error) {
    setProgress("reply", 0, error.message);
    showError(error.message);
  } finally {
    $("#tweet-detail-button").disabled = false;
  }
}

function renderTweetDetail(record) {
  const tweet = record.tweet || {};
  const username = tweet.author?.screen_name || "unknown";
  const avatar = tweet.author?.profile_image_url;
  $("#tweet-detail-content").className = "";
  $("#tweet-detail-content").innerHTML = `
    <div class="account-header">
      ${avatar ? `<img class="account-avatar" src="${escapeAttribute(avatar)}">` : '<div class="mini-mark">T</div>'}
      <div>
        <p class="eyebrow">FOCAL TWEET · ${escapeHTML(tweet.id || "")}</p>
        <h2>@${escapeHTML(username)}</h2>
        <p>${escapeHTML(tweet.text || "")}</p>
      </div>
    </div>
    <div class="details-grid">${(record.sections || []).map(section => sectionHTML(section)).join("")}</div>`;
}

async function handleMenuAction(action) {
  if (action === "logs") return openLogs();
  if (action === "export-data") return exportPosts();
  if (action === "export-analytics") return showError("Tweet analytics export will activate when the Python ML worker is connected. Raw collection stays isolated in Go.");
  if (action === "export-authors") return exportAuthors();
  if (action === "generate-report") return generateReport();
}

async function exportPosts() {
  if (!state.posts.length) return showError("Run a scan before exporting data.");
  const path = await window.trexDesktop.saveFile({
    defaultPath: `trex_posts_${dateStamp()}.xlsx`,
    filters: [{ name: "Excel Files", extensions: ["xlsx"] }]
  });
  if (!path) return;
  showProgress("Export Data", "Writing all extracted posts and search metadata…", 20);
  try {
    await api("/api/export/posts", { method: "POST", body: { path } });
    updateProgressDialog("Export complete.", 100);
    await wait(600);
    closeProgress();
  } catch (error) {
    updateProgressDialog(error.message, 0);
  }
}

async function exportCurrentAccount() {
  if (!state.account) return;
  const path = await window.trexDesktop.saveFile({
    defaultPath: `${state.account.screenName}_account_info.xlsx`,
    filters: [{ name: "Excel Files", extensions: ["xlsx"] }]
  });
  if (!path) return;
  showProgress("Export Account Data", "Writing parsed and raw GraphQL account fields…", 25);
  try {
    await api("/api/export/account", { method: "POST", body: { path, screenName: state.account.screenName } });
    updateProgressDialog("Account export complete.", 100);
    await wait(500);
    closeProgress();
  } catch (error) {
    updateProgressDialog(error.message, 0);
  }
}

async function exportAuthors() {
  if (!state.posts.length) return showError("Run a scan before exporting authors.");
  const path = await window.trexDesktop.saveFile({
    defaultPath: `trex_authors_${dateStamp()}.xlsx`,
    filters: [{ name: "Excel Files", extensions: ["xlsx"] }]
  });
  if (!path) return;
  showProgress("Export Authors Data", "Preparing direct author GraphQL requests…", 2);
  try {
    const response = await api("/api/export/authors", { method: "POST", body: { path } });
    await watchJob(response.jobId, job => updateProgressDialog(job.message, job.progress));
    updateProgressDialog("Authors export complete.", 100);
    await wait(600);
    closeProgress();
  } catch (error) {
    updateProgressDialog(error.message, 0);
  }
}

async function exportAccountPDF() {
  if (!state.account) return;
  const html = accountReportHTML(state.account);
  await window.trexDesktop.savePDF({ html, defaultPath: `${state.account.screenName}_account_report.pdf` });
}

async function generateReport() {
  if (!state.posts.length) return showError("Run a scan before generating a report.");
  showProgress("Generate Report", "Calculating collection statistics and date volume…", 35);
  const html = scanReportHTML(state.posts, state.scanContext);
  updateProgressDialog("Rendering PDF report…", 72);
  const path = await window.trexDesktop.savePDF({ html, defaultPath: `trex_collection_report_${dateStamp()}.pdf` });
  if (path) {
    updateProgressDialog("Report generated.", 100);
    await wait(500);
  }
  closeProgress();
}

async function openLogs() {
  const result = await api("/api/logs");
  state.logs = result.entries || [];
  $("#logs-output").value = state.logs.join("\n");
  $("#logs-dialog").showModal();
  $("#logs-output").scrollTop = $("#logs-output").scrollHeight;
}

function connectEvents() {
  const url = state.backendURL.replace(/^http/, "ws") + "/ws";
  state.ws = new WebSocket(url);
  state.ws.onmessage = event => {
    const message = JSON.parse(event.data);
    if (message.type === "log") {
      state.logs.push(`[${new Date(message.timestamp).toLocaleString()}] [${message.level || "INFO"}] ${message.message}`);
      if ($("#logs-dialog").open) {
        $("#logs-output").value = state.logs.join("\n");
        $("#logs-output").scrollTop = $("#logs-output").scrollHeight;
      }
    }
    if (message.type === "post" && message.jobId === state.currentJob && message.data) {
      state.posts.push(message.data);
      if (state.posts.length <= 25) renderPosts(state.posts.slice(0, 25), state.posts.length);
      else $("#post-count").textContent = state.posts.length;
    }
    if (message.type === "post" && message.jobId === state.trackerJob && message.data) {
      if (!state.trackerPosts.some(post => post.id === message.data.id)) {
        state.trackerPosts.unshift(message.data);
        renderTrackerPosts();
      }
    }
  };
  state.ws.onclose = () => setTimeout(connectEvents, 1200);
}

async function watchJob(jobId, onUpdate) {
  while (true) {
    const job = await api(`/api/jobs/${jobId}`);
    onUpdate?.(job);
    if (job.status === "completed") return job;
    if (job.status === "failed") throw new Error(job.error || job.message);
    if (job.status === "cancelled") throw new Error("Operation cancelled.");
    await wait(450);
  }
}

async function api(path, options = {}) {
  const request = { method: options.method || "GET", headers: {} };
  if (options.body !== undefined) {
    request.headers["Content-Type"] = "application/json";
    request.body = JSON.stringify(options.body);
  }
  const response = await fetch(state.backendURL + path, request);
  const text = await response.text();
  let data = {};
  try { data = text ? JSON.parse(text) : {}; } catch { data = { error: text }; }
  if (!response.ok) throw new Error(data.error || `Backend returned HTTP ${response.status}`);
  return data;
}

function showRecordDialog(title, value, eyebrow = "DETAILS") {
  $("#dialog-eyebrow").textContent = eyebrow;
  $("#dialog-title").textContent = title;
  const rows = flatten(value);
  $("#dialog-body").innerHTML = `<section class="details-section">${Object.entries(rows).map(([key, item]) =>
    `<div class="field-row"><span class="field-label">${escapeHTML(humanize(key))}</span><span class="field-value">${formatValue(item)}</span></div>`
  ).join("")}</section>`;
  $("#details-dialog").showModal();
}

function showProgress(title, status, progress) {
  $("#progress-title").textContent = title;
  updateProgressDialog(status, progress);
  $("#progress-dialog").showModal();
}

function updateProgressDialog(status, progress) {
  $("#dialog-progress").style.width = `${Math.max(0, Math.min(100, progress))}%`;
  $("#dialog-status").textContent = status;
}

function closeProgress() {
  if ($("#progress-dialog").open) $("#progress-dialog").close();
}

function setProgress(prefix, progress, status) {
  $(`#${prefix}-progress`).style.width = `${Math.max(0, Math.min(100, progress))}%`;
  $(`#${prefix}-status`).textContent = status;
}

function showError(message) {
  $("#dialog-eyebrow").textContent = "NOTICE";
  $("#dialog-title").textContent = "Action unavailable";
  $("#dialog-body").innerHTML = `<div class="empty-state">${escapeHTML(message)}</div>`;
  $("#details-dialog").showModal();
}

function setDefaultDates() {
  const today = new Date();
  const before = new Date(today);
  before.setDate(today.getDate() - 7);
  $("#to-date").value = today.toISOString().slice(0, 10);
  $("#from-date").value = before.toISOString().slice(0, 10);
}

function selected(name) {
  return document.querySelector(`input[name="${name}"]:checked`)?.value || "";
}

function commaValues(value) {
  return value.split(",").map(item => item.trim()).filter(Boolean);
}

function cleanScreenName(value) {
  return value.trim().replace(/^https?:\/\/(www\.)?(x|twitter)\.com\//i, "").replace(/^@/, "").split(/[/?]/)[0];
}

function flatten(value, prefix = "", result = {}) {
  if (Array.isArray(value)) {
    value.forEach((child, index) => flatten(child, prefix ? `${prefix}.${index + 1}` : `${index + 1}`, result));
  } else if (value && typeof value === "object") {
    Object.entries(value).forEach(([key, child]) => flatten(child, prefix ? `${prefix}.${key}` : key, result));
  } else if (value !== null && value !== "" && value !== undefined) {
    result[prefix] = value;
  }
  return result;
}

function humanize(value) {
  return value.replace(/[._]/g, " ").replace(/\b\w/g, letter => letter.toUpperCase());
}

function formatValue(value) {
  if (value === true) return "Yes";
  if (value === false) return "No";
  if (Array.isArray(value)) return escapeHTML(value.join(", "));
  if (value && typeof value === "object") return escapeHTML(JSON.stringify(value));
  const text = String(value ?? "");
  if (/^https?:\/\//.test(text)) return `<a href="${escapeAttribute(text)}" target="_blank">${escapeHTML(text)}</a>`;
  return escapeHTML(text);
}

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, char => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char]);
}

function escapeAttribute(value) {
  return escapeHTML(value);
}

function wait(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

function dateStamp() {
  return new Date().toISOString().replace(/[:T]/g, "-").slice(0, 19);
}

function scanReportHTML(posts, context) {
  const byDate = {};
  const authors = {};
  const totals = { likes: 0, replies: 0, reposts: 0, views: 0 };
  posts.forEach(post => {
    const date = parsePostDate(post.createdAt);
    if (date) byDate[date] = (byDate[date] || 0) + 1;
    const author = post.author?.screen_name || "unknown";
    authors[author] = (authors[author] || 0) + 1;
    totals.likes += number(post.metrics?.like_count);
    totals.replies += number(post.metrics?.reply_count);
    totals.reposts += number(post.metrics?.retweet_count);
    totals.views += number(post.metrics?.view_count);
  });
  const dates = Object.entries(byDate).sort(([a], [b]) => a.localeCompare(b));
  const max = Math.max(...dates.map(([, count]) => count), 1);
  const topAuthors = Object.entries(authors).sort((a, b) => b[1] - a[1]).slice(0, 12);
  const terms = context?.mode === "custom" ? context.customQuery : context?.terms?.join(", ");
  return reportDocument("T-REX Collection Report", `
    <section class="hero"><p>Generated ${new Date().toLocaleString()}</p><h1>Twitter Collection Intelligence Report</h1><p>${escapeHTML(terms || "Search context unavailable")}</p></section>
    <section><h2>Collection Summary</h2><div class="stats">
      ${stat("Total posts", posts.length)}${stat("Likes", totals.likes)}${stat("Replies", totals.replies)}${stat("Reposts", totals.reposts)}${stat("Views", totals.views)}
    </div></section>
    <section><h2>Tweet Volume by Date</h2><div class="chart">${dates.map(([date, count]) => `<div class="bar-row"><span>${date}</span><div><i style="width:${count / max * 100}%"></i></div><b>${count}</b></div>`).join("")}</div></section>
    <section><h2>Date Metrics</h2>${table(["Date", "Tweets", "Share"], dates.map(([date, count]) => [date, count, `${(count / posts.length * 100).toFixed(2)}%`]))}</section>
    <section><h2>Top Authors</h2>${table(["Author", "Extracted tweets"], topAuthors.map(([author, count]) => [`@${author}`, count]))}</section>
    <section><h2>Analysis Boundary</h2><p>Individual sentiment, emotion, toxicity and NER fields are intentionally omitted until the isolated Python ML worker is connected.</p></section>
  `);
}

function accountReportHTML(account) {
  return reportDocument(`${account.name} Account Report`, `
    <section class="hero account-hero">
      ${account.avatarUrl ? `<img src="${escapeAttribute(account.avatarUrl)}">` : ""}
      <div><p>Generated ${new Date().toLocaleString()}</p><h1>${escapeHTML(account.name)}</h1><h3>@${escapeHTML(account.screenName)}</h3></div>
    </section>
    ${(account.sections || []).map(section => `<section><h2>${escapeHTML(section.title)}</h2>${table(["Field", "Value"], section.rows.map(row => [row.label, printable(row.value)]))}</section>`).join("")}
  `);
}

function reportDocument(title, content) {
  return `<!doctype html><html><head><meta charset="utf-8"><title>${escapeHTML(title)}</title><style>
    @page{size:A4;margin:14mm}*{box-sizing:border-box}body{margin:0;color:#17191b;font:10pt/1.5 Arial,sans-serif}
    .hero{padding:24px;margin-bottom:20px;background:#20242a;color:white;border-radius:9px}.hero h1{font-size:23pt;margin:4px 0}.hero p{margin:3px 0;color:#c9cfd4}
    .account-hero{display:flex;gap:18px;align-items:center}.account-hero img{width:84px;height:84px;border-radius:50%;object-fit:cover}
    section{break-inside:avoid;margin-bottom:18px}h2{font-size:14pt;border-bottom:2px solid #20242a;padding-bottom:5px}.stats{display:flex;gap:8px;flex-wrap:wrap}.stat{flex:1;min-width:100px;padding:12px;background:#f0f2f3;border:1px solid #d5dadd}.stat b{display:block;font-size:16pt}.stat span{color:#687079}
    table{width:100%;border-collapse:collapse}th{background:#20242a;color:white;text-align:left}th,td{padding:7px;border:1px solid #d6dade;vertical-align:top}tr:nth-child(even)td{background:#f4f5f6}
    .bar-row{display:grid;grid-template-columns:85px 1fr 42px;gap:8px;align-items:center;margin:7px 0}.bar-row div{height:16px;background:#edf0f2}.bar-row i{display:block;height:100%;background:#59636f}.bar-row b{text-align:right}
  </style></head><body>${content}</body></html>`;
}

function stat(label, value) {
  return `<div class="stat"><b>${Number(value || 0).toLocaleString()}</b><span>${escapeHTML(label)}</span></div>`;
}

function table(headers, rows) {
  return `<table><thead><tr>${headers.map(item => `<th>${escapeHTML(item)}</th>`).join("")}</tr></thead><tbody>${rows.map(row => `<tr>${row.map(item => `<td>${escapeHTML(item)}</td>`).join("")}</tr>`).join("")}</tbody></table>`;
}

function printable(value) {
  if (value === true) return "Yes";
  if (value === false) return "No";
  if (Array.isArray(value)) return value.join(", ");
  if (value && typeof value === "object") return JSON.stringify(value);
  return String(value ?? "");
}

function number(value) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function parsePostDate(value) {
  const match = String(value || "").match(/\d{4}-\d{2}-\d{2}/);
  if (match) return match[0];
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "" : date.toISOString().slice(0, 10);
}
