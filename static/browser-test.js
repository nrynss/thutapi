const results = document.querySelector("#results");
const originalFetch = window.fetch;
const originalAudio = window.Audio;
const originalEventSource = window.EventSource;
const originalPushState = window.history.pushState;
const eventSources = [];
const calls = [];
let audioCreated = 0;
let audioPlaySources = [];
let activeState;

function isSilentPCM16WAV(source) {
  const prefix = "data:audio/wav;base64,";
  if (!source.startsWith(prefix)) return false;
  let bytes;
  try {
    const encoded = atob(source.slice(prefix.length));
    bytes = Uint8Array.from(encoded, character => character.charCodeAt(0));
  } catch (_) {
    return false;
  }
  if (bytes.length < 44) return false;
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const text = offset => String.fromCharCode(...bytes.slice(offset, offset + 4));
  const dataSize = view.getUint32(40, true);
  const blockAlign = view.getUint16(32, true);
  if (text(0) !== "RIFF" || text(8) !== "WAVE" || text(12) !== "fmt " || text(36) !== "data") return false;
  if (view.getUint32(4, true) !== bytes.length - 8 || view.getUint32(16, true) !== 16) return false;
  if (view.getUint16(20, true) !== 1 || view.getUint16(22, true) !== 1 || view.getUint16(34, true) !== 16) return false;
  if (blockAlign !== 2 || dataSize < blockAlign || dataSize % blockAlign !== 0 || bytes.length !== 44 + dataSize) return false;
  return bytes.slice(44).every(byte => byte === 0);
}

class FakeAudio {
  constructor() { audioCreated++; this.src = ""; this.muted = false; this.currentTime = 0; }
  setAttribute() {}
  play() {
    audioPlaySources.push(this.src);
    if (!this.src) return Promise.reject(new Error("empty audio source"));
    if (this.src.startsWith("data:audio/wav;base64,") && !isSilentPCM16WAV(this.src)) return Promise.reject(new Error("invalid silent wav"));
    return Promise.resolve();
  }
  pause() {}
}

class FakeEventSource {
  constructor(url) {
    this.url = url;
    this.listeners = new Map();
    eventSources.push(this);
    if (activeState.openingEvent) queueMicrotask(() => this.dispatch("question", activeState.openingEvent));
  }
  addEventListener(name, fn) { this.listeners.set(name, fn); }
  dispatch(name, data) { this.listeners.get(name)?.({ data: JSON.stringify(data) }); }
  close() { this.closed = true; }
}

function response(body, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

function configure(state) {
  activeState = state;
  calls.length = 0;
  eventSources.length = 0;
  audioCreated = 0;
  audioPlaySources = [];
  window.Audio = FakeAudio;
  window.EventSource = FakeEventSource;
  window.fetch = async (url, init = {}) => {
    calls.push({ url, init });
    const id = activeState.id || "demo";
    if (url === `/interviews/${id}` && activeState.catchup) return activeState.catchup;
    if (activeState.responses?.[url]) return activeState.responses[url];
    if (url === `/interviews/${id}/events`) return response({});
    if (url === `/interviews/${id}`) return response(activeState);
    if (url === `/interviews/${id}/generate`) return response({ events_url: `/interviews/${id}/generate/events`, book_id: "book" }, 202);
    if (url === "/interviews") return response({ id: "demo", book_id: "book", events_url: "/interviews/demo/events", opening: activeState.startOpening }, 201);
    return response({}, 200);
  };
  document.querySelector("#app")?.remove();
  const app = document.createElement("main");
  app.id = "app";
  app.dataset.interviewId = state.route || state.id || "demo";
  document.body.append(app);
  return import(`/static/app.js?browser-test=${Math.random()}`);
}

function check(condition, message) {
  if (!condition) throw new Error(message);
}

async function settle() { await new Promise(resolve => setTimeout(resolve, 30)); }

async function waitFor(checker, message) {
  for (let attempt = 0; attempt < 20; attempt++) {
    if (checker()) return;
    await settle();
  }
  throw new Error(message);
}

async function testOpeningErrorRecovery() {
  results.textContent = "opening error…";
  await configure({ status: "open", error: "internal", turns: [] });
  await settle();
  check(document.querySelector('[role="status"]'), "opening error has no warm recovery message");
  check(!document.querySelector(".answer button").disabled, "opening error leaves Send disabled");
}

async function testCatchupAndAudio() {
  results.textContent = "catch-up…";
  sessionStorage.removeItem("thutapi:question:demo");
  await configure({ status: "open", turns: [{ role: "interviewer", text: "Who is there?" }], current: { turn: 1, text: "Who is there?", chips: ["A fox"] } });
  await settle();
  check(document.querySelectorAll(".chips button").length === 1, "catch-up lost the saved chip");
  const source = eventSources[0];
  source.dispatch("question", { turn: 1, text: "Who is there?", chips: ["A fox"] });
  source.dispatch("question_audio", { turn: 1, audio_url: "/media/audio" });
  await settle();
  check(document.querySelector(".speaker"), "current-turn audio control disappeared");
  check(document.querySelectorAll(".chips button").length === 1, "duplicate question replaced catch-up chips");
}

async function testAudioUnlockContract() {
  results.textContent = "audio unlock…";
  await configure({ route: "shelf" });
  document.querySelector("[data-start]").click();
  await settle();
  check(audioCreated === 1, "shelf CTA did not create exactly one audio element");
  check(audioPlaySources[0]?.startsWith("data:audio/wav;base64,"), "shelf CTA tried to unlock audio without a playable source");
  const form = document.querySelector("form");
  form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  await waitFor(() => eventSources.length === 1, "opening audio stream was not subscribed");
  const source = eventSources[0];
  source.dispatch("question", { turn: 1, text: "Who is there?", chips: ["A fox"] });
  source.dispatch("question_audio", { turn: 1, audio_url: "/media/audio" });
  await settle();
  check(document.querySelector(".speaker")?.textContent === "Listen again", "successful shelf unlock still claimed that a speaker tap was required");
  check(audioPlaySources.includes("/media/audio"), "question audio was not played after the real gesture-time unlock");
}

async function testOpeningQuestionHandoff() {
  results.textContent = "opening question hand-off…";
  sessionStorage.removeItem("thutapi:question:demo");
  await configure({
    route: "new",
    id: "demo",
    status: "open",
    turns: [{ role: "interviewer", text: "Who is there?" }],
    openingEvent: { turn: 1, text: "Who is there?", chips: ["A fox"] }
  });
  await settle();
  document.querySelector("form").dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  await waitFor(() => document.querySelectorAll(".chips button").length === 1, "opening event lost its chip during route hand-off");
}

async function testLiveQuestionWinsCatchup() {
  results.textContent = "live question ordering…";
  sessionStorage.removeItem("thutapi:question:ordering");
  let resolveCatchup;
  const catchup = new Promise(resolve => { resolveCatchup = resolve; });
  await configure({ id: "ordering", status: "open", catchup });
  const source = eventSources[0];
  source.dispatch("question", { turn: 1, text: "Who is there?", chips: ["A fox"] });
  await settle();
  resolveCatchup(response({ status: "open", turns: [{ role: "interviewer", text: "Who is there?" }] }));
  await settle();
  check(document.querySelectorAll(".chips button").length === 1, "equal catch-up turn erased live chips");
}

async function testMissingCatchupRecovery() {
  results.textContent = "missing story recovery…";
  await configure({ id: "missing", status: "open", responses: { "/interviews/missing": response({ error: "not_found" }, 404) } });
  await settle();
  check(document.querySelector('[role="status"]')?.textContent.includes("wandered away"), "missing catch-up is not warm");
  check(document.querySelector(".doors .primary")?.textContent.includes("Start a new story"), "missing catch-up has no new-story door");
  check(!document.querySelector(".answer"), "missing catch-up still offers repeat-failing Send");
}

async function testEndedCatchupClosesInterviewStream() {
  results.textContent = "terminal catch-up lifecycle…";
  await configure({ id: "ended", status: "ended", turns: [{ role: "closing", text: "Goodbye" }] });
  await waitFor(() => eventSources.length >= 2 && calls.some(call => call.url.endsWith("/generate")), "ended interview did not start generation");
  check(eventSources[0].closed === true, "catch-up-ended interview EventSource remained open");
}

async function testShelfGestureAndGeneration() {
  results.textContent = "shelf and generation…";
  await configure({ route: "shelf" });
  await settle();
  document.querySelector("[data-start]").click();
  check(audioCreated === 1, "shelf CTA did not create exactly one audio element");
  check(location.pathname === "/interview/new", "shelf CTA discarded the same document flow");
  await configure({ status: "ended", turns: [{ role: "closing", text: "Goodbye" }] });
  await waitFor(() => calls.some(call => call.url.endsWith("/generate")), "ended interview did not take the no-sample generation path");
  check(!document.querySelector("[data-voice-record], [data-voice-upload], [data-voice-capture]"), "T13 capture controls render before T13 mounts them");
  check(document.querySelector("iframe.race-frame")?.src.includes("/static/race/race.html"), "generation screen did not consume the T9a race widget");
  const source = eventSources.at(-1);
  source.dispatch("page_approved", { n: 8, image_url: "/media/page-8" });
  await settle();
  check(document.querySelector("iframe.race-frame")?.title === "1 of 8 pages finished", "first out-of-order approval did not count as one page");
  source.dispatch("page_approved", { n: 1, image_url: "/media/page-1" });
  await settle();
  check(document.querySelector("iframe.race-frame")?.title === "2 of 8 pages finished", "distinct approvals did not increment the race count");
  const frame = document.querySelector("iframe.race-frame");
  await waitFor(() => frame.contentDocument?.querySelector(".race"), "race iframe did not load for bridge test");
  await settle();
  check(frame.contentDocument.querySelector(".race").style.getPropertyValue("--done") === "2", "parent progress did not reach the T9a iframe bridge");
}

async function run() {
  try {
    await testOpeningErrorRecovery();
    await testOpeningQuestionHandoff();
    await testCatchupAndAudio();
    await testAudioUnlockContract();
    await testLiveQuestionWinsCatchup();
    await testMissingCatchupRecovery();
    await testEndedCatchupClosesInterviewStream();
    await testShelfGestureAndGeneration();
    results.innerHTML = "<h1>PASS</h1><pre>opening and terminal catch-up recovery\ncatch-up ordering, chips, and current-turn audio\nshelf unlock, no-sample route, distinct approval progress, and iframe bridge</pre>";
  } catch (error) {
    results.innerHTML = `<h1>FAIL</h1><pre>${String(error.stack || error)}</pre>`;
  } finally {
    window.fetch = originalFetch;
    window.Audio = originalAudio;
    window.EventSource = originalEventSource;
    window.history.pushState = originalPushState;
  }
}

run();
