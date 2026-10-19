import { CardStack } from "./cards.js";

const $ = selector => document.querySelector(selector);
const $$ = selector => [...document.querySelectorAll(selector)];
let session = null;
let levels = [];
let busy = false;
let pending = null;
let screen = "setup";
const cards = new CardStack({
  flip: () => cards.setFlipped(!cards.flipped),
  grade,
  isBusy: () => busy || screen !== "practice",
});

function show(name) {
  screen = name;
  ["setup", "practice", "complete"].forEach(id => { $(`#${id}`).hidden = name !== id; });
  $("#leave").hidden = name !== "practice";
  $("#loading").hidden = true;
}

function setBusy(value) {
  busy = value;
  $$("#wrong, #right, #undo, #card, #leave, #resume, #end, #again, #complete-undo, #retry, #refresh")
    .forEach(button => { button.disabled = value; });
  $("#settings-fields").disabled = value || Boolean(session && ["active", "paused"].includes(session.status));
  if (!value) {
    $("#undo").disabled = !session?.canUndo;
    $("#complete-undo").disabled = !session?.canUndo;
  }
  $("#saving").textContent = value && screen === "practice" ? "Saving your progress…" : "";
}

function clearError() {
  $("#message").hidden = true;
  $("#retry").hidden = true;
  $("#refresh").hidden = true;
}

function showError(error) {
  $("#message-text").textContent = error.message;
  $("#message").hidden = false;
  $("#retry").hidden = !pending || error.status === 409;
  $("#refresh").hidden = false;
}

async function request(path, options = {}) {
  let response;
  try {
    response = await fetch(path, {
      ...options,
      headers: { "Content-Type": "application/json", ...options.headers },
      signal: AbortSignal.timeout(20000),
    });
  } catch (error) {
    throw new Error(`Could not reach Wren. Your last confirmed progress is safe. ${error.message}`);
  }
  const type = response.headers.get("Content-Type") || "";
  const data = type.includes("application/json") ? await response.json() : null;
  if (!response.ok) {
    const error = new Error(data?.error || `Request failed (${response.status}). Reload progress before continuing.`);
    error.status = response.status;
    throw error;
  }
  return data;
}

function readSettings() {
  return {
    level: Number($("#level").value),
    includeLower: $("#include-lower").checked,
    batchSize: Number($("#batch-size").value),
    directions: $$("input[name=direction]:checked").map(input => input.value),
    mode: $("input[name=mode]:checked").value,
  };
}

function updateHints() {
  const settings = readSettings();
  const selected = levels.filter(level => settings.includeLower ?
    level.level <= settings.level : level.level === settings.level);
  const total = selected.reduce((sum, level) => sum + level.total, 0);
  const available = selected.reduce((sum, level) => sum + level.available, 0);
  $("#availability").textContent = `${available.toLocaleString()} available words` +
    (total > available ? ` · ${(total - available).toLocaleString()} incomplete entries excluded.` : " · All entries ready.");
  $("#mode-hint").textContent = settings.mode === "assessment" ?
    "Find your gaps. Known cards return in 7 days; unknown cards return sooner." :
    "Due reviews first, then new cards. Missed cards return to the stack.";
}

function applySettings(settings) {
  $("#level").replaceChildren(...levels.map(level => {
    const option = document.createElement("option");
    option.value = String(level.level);
    option.textContent = level.label;
    return option;
  }));
  $("#level").value = String(settings.level);
  $("#include-lower").checked = settings.includeLower;
  $("#batch-size").value = String(settings.batchSize);
  $$("input[name=direction]").forEach(input => { input.checked = settings.directions.includes(input.value); });
  $$("input[name=mode]").forEach(input => { input.checked = input.value === settings.mode; });
  updateHints();
}

function setupView() {
  show("setup");
  const unfinished = session && ["active", "paused"].includes(session.status);
  $("#resume-panel").hidden = !unfinished;
  $("#settings").hidden = Boolean(unfinished);
  if (unfinished) {
    $("#resume-detail").textContent = `${session.remaining} of ${session.initialSize} cards remain. Your stack is just where you left it.`;
  }
  setBusy(false);
}

function renderSession() {
  if (!session) return setupView();
  if (session.status === "ended") return setupView();
  if (session.status === "complete") {
    show("complete");
    $("#complete-detail").textContent = `${session.initialSize} cards cleared. Each direction has its own next review.`;
    $("#complete-undo").hidden = !session.canUndo;
  } else {
    show("practice");
    const settings = session.settings;
    const group = settings.level === 7 ? "7–9" : settings.level;
    const label = settings.includeLower && settings.level > 1 ? `Levels 1–${settings.level === 7 ? 9 : settings.level}` : `HSK ${group}`;
    const directions = settings.directions.map(direction => ({ read: "Read", write: "Write", pinyin: "Pinyin" })[direction]).join(", ");
    $("#session-description").textContent = `${settings.mode === "assessment" ? "Initial assessment" : "Training"} · ${directions} · ${label}`;
    $("#notice").textContent = session.notice || "";
    if (session.current) cards.render(session.current, session.remaining);
    $("#progress").max = session.initialSize;
    $("#progress").value = session.cleared;
    $("#cleared").textContent = `${session.cleared} of ${session.initialSize} cards cleared`;
    $("#remaining").textContent = `${session.remaining} remaining · includes retries`;
  }
  setBusy(false);
}

async function load() {
  setBusy(true);
  clearError();
  try {
    const [configuration, current] = await Promise.all([
      request("/api/settings"), request("/api/sessions/current"),
    ]);
    levels = configuration.levels;
    applySettings(configuration.settings);
    session = current;
    pending = null;
    if (session?.status === "active") renderSession();
    else setupView();
  } catch (error) {
    $("#loading").textContent = "Could not load your practice.";
    showError(error);
  } finally {
    setBusy(false);
  }
}

async function perform(path, body, { animate = null, destination = "session" } = {}) {
  if (busy) return;
  clearError();
  const operation = { path, body, destination };
  pending = operation;
  const wasFlipped = cards.flipped;
  setBusy(true);
  try {
    // Wait for the animation even on request failure so it cannot hide a restored card.
    const [response, animation] = await Promise.allSettled([
      request(path, { method: "POST", body: JSON.stringify(body) }),
      animate ? animate() : Promise.resolve(),
    ]);
    if (response.status === "rejected") throw response.reason;
    session = response.value;
    pending = null;
    if (animation.status === "rejected") console.error("Card animation failed", animation.reason);
    if (destination === "setup") setupView();
    else renderSession();
  } catch (error) {
    if (session?.current && screen === "practice") {
      cards.render(session.current, session.remaining);
      cards.setFlipped(wasFlipped);
    }
    showError(error);
    if (error.status === 400 || error.status === 404) {
      pending = null;
      $("#retry").hidden = true;
    }
    // A lost response might already have committed. Do not permit a new ID to
    // grade the same card until this exact mutation is retried or state reloaded.
  } finally {
    setBusy(false);
    if (pending) {
      $$("#wrong, #right, #undo, #leave, #end, #again, #complete-undo, #resume")
        .forEach(button => { button.disabled = true; });
      $("#settings-fields").disabled = true;
    }
  }
}

function mutation(extra = {}) {
  return { mutationId: mutationID(), revision: session.revision, ...extra };
}

function mutationID() {
  // crypto.randomUUID requires a secure origin; LAN HTTP still supports getRandomValues.
  return [...crypto.getRandomValues(new Uint8Array(16))]
    .map(value => value.toString(16).padStart(2, "0")).join("");
}

async function grade(right) {
  if (busy || pending || screen !== "practice" || !session?.current) return;
  await perform(`/api/sessions/${session.id}/answers`, mutation({
    cardId: session.current.id, right,
  }), { animate: () => cards.dismiss(right, session.remaining === 1) });
}

async function undo() {
  if (busy || pending || !session?.canUndo) return;
  await perform(`/api/sessions/${session.id}/undo`, mutation());
}

$("#settings").addEventListener("submit", event => {
  event.preventDefault();
  if (busy || pending) return;
  const settings = readSettings();
  if (settings.directions.length === 0) {
    showError(new Error("Select at least one card direction."));
    return;
  }
  void perform("/api/sessions", { settings, mutationId: mutationID() });
});
$("#settings").addEventListener("change", updateHints);
$("#wrong").addEventListener("click", () => { void grade(false); });
$("#right").addEventListener("click", () => { void grade(true); });
$("#undo").addEventListener("click", undo);
$("#complete-undo").addEventListener("click", undo);
$("#resume").addEventListener("click", renderSession);
$("#leave").addEventListener("click", () => {
  if (session) void perform(`/api/sessions/${session.id}/pause`, mutation(), { destination: "setup" });
});
$("#end").addEventListener("click", () => {
  if (session && confirm("End this session? Saved learning progress is kept; unfinished cards remain eligible for later practice.")) {
    void perform(`/api/sessions/${session.id}/end`, mutation(), { destination: "setup" });
  }
});
$("#again").addEventListener("click", () => { session = null; setupView(); });
$("#retry").addEventListener("click", () => {
  if (pending) void perform(pending.path, pending.body, { destination: pending.destination });
  else void load();
});
$("#refresh").addEventListener("click", load);

document.addEventListener("keydown", event => {
  if (busy || pending || event.altKey || event.ctrlKey || event.metaKey ||
      event.target.closest("input, select, textarea, [contenteditable=true]")) return;
  if (screen !== "practice") return;
  if (event.code === "Space" && !event.target.closest("button, a")) {
    event.preventDefault();
    if (!event.repeat) cards.setFlipped(!cards.flipped);
  } else if (event.code === "ArrowLeft" || event.code === "ArrowRight") {
    event.preventDefault();
    if (!event.repeat) void grade(event.code === "ArrowRight");
  } else if (event.key.toLowerCase() === "z") {
    event.preventDefault();
    if (!event.repeat) void undo();
  }
});

void load();
