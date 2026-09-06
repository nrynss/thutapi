const results = document.querySelector("#results");
const originalFetch = window.fetch;
const originalAudio = window.Audio;
const originalEventSource = window.EventSource;
const originalMediaRecorder = window.MediaRecorder;
const originalMediaDevices = navigator.mediaDevices;
const originalPushState = window.history.pushState;
// Where a finished book WOULD have sent the reader. The app leaves its
// single-page flow through one seam (app.js setBookExit) precisely so this
// can be recorded instead of performed: a real navigation unloads this page
// and ends the run mid-suite without reporting anything.
let bookExit = "";
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
    if (!activeState.manualEventSourceOpen) queueMicrotask(() => this.dispatch("open"));
    // Capture the opening event NOW. Reading activeState inside the
    // microtask let a later configure() replace it, so a stream opened by
    // one test dispatched a "question" carrying undefined to the next
    // test's app.
    const opening = activeState.openingEvent;
    if (opening) queueMicrotask(() => this.dispatch("question", opening));
  }
  addEventListener(name, fn) { this.listeners.set(name, fn); }
  dispatch(name, data) { this.listeners.get(name)?.({ data: JSON.stringify(data) }); }
  drop(name, data) { (this.dropped ||= []).push({ name, data }); }
  close() { this.closed = true; }
}

function response(body, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

function configure(state) {
  // Close everything the previous test left running before adopting the new
  // state. Each configure() mounts a FRESH app module, but the previous
  // module's effects, streams and in-flight reads keep going: without this,
  // one test's book stream delivers into the next test's app and the two
  // fight over the same screen.
  for (const source of eventSources) source.close();
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
    if (activeState.responses?.[url]) {
      const configured = activeState.responses[url];
      return typeof configured === "function" ? configured() : configured;
    }
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
  bookExit = "";
  return import(`/static/app.js?browser-test=${Math.random()}`).then(module => {
    module.setBookExit(url => { bookExit = url; });
    return module;
  });
}

function check(condition, message) {
  if (!condition) throw new Error(message);
}

async function settle() { await new Promise(resolve => setTimeout(resolve, 30)); }

// waitFor polls against a WALL-CLOCK deadline, not a fixed number of ticks.
// A browser throttles timers in a hidden tab — to roughly one per minute —
// so a twenty-tick budget there is not 600 ms but twenty minutes, which
// reads as a hung suite rather than a failing one. A deadline fails at the
// time it says it will whether the tab is visible or not.
const WAIT_MS = 5000;

async function waitFor(checker, message) {
  const deadline = Date.now() + WAIT_MS;
  for (;;) {
    if (checker()) return;
    if (Date.now() >= deadline) throw new Error(message);
    await settle();
  }
}

// reachVoiceStep walks the closing screen to the adult voice step. The
// interview no longer slides into that step on its own: a closing message
// used to be presented as an open question with a text box under it (Live
// bug report 1), and the fix put a single "Make my book" door there
// instead, which the child taps. Every test that wants the voice step has
// to tap it too.
async function reachVoiceStep() {
  await waitFor(
    () => document.querySelector("[data-voice-library]") || document.querySelector("[data-make-book]"),
    "neither the closing door nor the adult voice step rendered"
  );
  const door = document.querySelector("[data-make-book]");
  if (door) door.click();
  await waitFor(() => document.querySelector("[data-voice-library]"), "adult voice step did not render");
}

// firstSource waits for the app to open its first EventSource. Reading
// eventSources[0] straight after configure() races the module import and
// the first render, and reads undefined on a slow machine.
async function firstSource() {
  await waitFor(() => eventSources.length > 0, "no event stream was opened");
  return eventSources[0];
}

// A reload of an interview that ALREADY has a book row goes straight to
// the wait screen and recovers its running generation: the voice step is
// behind it, not ahead of it. Those tests wait for the recovery instead of
// walking a step the app will never show them.
async function awaitGenerationRecovery() {
  await waitFor(() => document.querySelector(".wait"), "reload did not reach the generation screen");
}

async function skipVoiceSample() {
  await reachVoiceStep();
  document.querySelector("[data-voice-library]").click();
}

async function testOpeningErrorRecovery() {
  results.textContent = "opening error…";
  await configure({ status: "open", error: "internal", turns: [] });
  // waitFor, not one settle(): the recovery notice lands only after the
  // module import, the first render and the catch-up read have all
  // resolved, and a single 30 ms tick is not reliably long enough for all
  // three on a cold import.
  await waitFor(() => document.querySelector('[role="status"]'), "opening error has no warm recovery message");
  check(!document.querySelector(".answer button").disabled, "opening error leaves Send disabled");
}

async function testCatchupAndAudio() {
  results.textContent = "catch-up…";
  sessionStorage.removeItem("thutapi:question:demo");
  await configure({ status: "open", turns: [{ role: "interviewer", text: "Who is there?" }], current: { turn: 1, text: "Who is there?", chips: ["A fox"] } });
  // The catch-up read is a promise: wait for its result rather than for a
  // fixed tick, or this asserts against the pre-catch-up render.
  await waitFor(() => document.querySelectorAll(".chips button").length === 1, "catch-up lost the saved chip");
  const source = await firstSource();
  source.dispatch("question", { turn: 1, text: "Who is there?", chips: ["A fox"] });
  source.dispatch("question_audio", { turn: 1, audio_url: "/media/audio" });
  await waitFor(() => document.querySelector(".speaker"), "current-turn audio control disappeared");
  check(document.querySelectorAll(".chips button").length === 1, "duplicate question replaced catch-up chips");
}

async function testAudioUnlockContract() {
  results.textContent = "audio unlock…";
  await configure({ route: "shelf" });
  await waitFor(() => document.querySelector("[data-start]"), "shelf did not render");
  document.querySelector("[data-start]").click();
  await waitFor(() => audioPlaySources.length > 0, "shelf CTA never tried to unlock audio");
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
  const source = await firstSource();
  source.dispatch("question", { turn: 1, text: "Who is there?", chips: ["A fox"] });
  await waitFor(() => document.querySelectorAll(".chips button").length === 1, "the live question never rendered its chip");
  resolveCatchup(response({ status: "open", turns: [{ role: "interviewer", text: "Who is there?" }] }));
  await settle();
  check(document.querySelectorAll(".chips button").length === 1, "equal catch-up turn erased live chips");
}

async function testMissingCatchupRecovery() {
  results.textContent = "missing story recovery…";
  await configure({ id: "missing", status: "open", responses: { "/interviews/missing": response({ error: "not_found" }, 404) } });
  // The headline carries the news and the live region carries the way
  // forward: assert both, not one phrase in whichever element happens to
  // hold it.
  await waitFor(() => document.querySelector(".card h1")?.textContent.includes("wandered away"), "missing catch-up never said the story wandered away");
  check(!!document.querySelector('[role="status"]'), "missing catch-up has no live region");
  check(document.querySelector('[role="status"]').textContent.includes("fresh little path"), "missing catch-up is not warm");
  check(document.querySelector(".doors .primary")?.textContent.includes("Start a new story"), "missing catch-up has no new-story door");
  check(!document.querySelector(".answer"), "missing catch-up still offers repeat-failing Send");
}

async function testEndedCatchupClosesInterviewStream() {
  results.textContent = "terminal catch-up lifecycle…";
  await configure({ id: "ended", status: "ended", turns: [{ role: "closing", text: "Goodbye" }] });
  await skipVoiceSample();
  await waitFor(() => eventSources.length >= 2 && calls.some(call => call.url.endsWith("/generate")), "ended interview did not start generation");
  check(eventSources[0].closed === true, "catch-up-ended interview EventSource remained open");
}

async function testEndedReloadCatchesUpRunningGeneration() {
  results.textContent = "running generation reload…";
  await configure({
    id: "reload",
    status: "ended",
    book_id: "book",
    turns: [{ role: "closing", text: "Goodbye" }],
    responses: {
      "/book/book/state": response({ status: "running", pages: [{ n: 3, image_url: "/media/page-3" }] }),
      "/interviews/reload/generate": response({ error: "busy" }, 409)
    }
  });
  await awaitGenerationRecovery();
  await waitFor(() => eventSources.length >= 2 && calls.some(call => call.url === "/book/book/state"), "reload did not subscribe then read C4 state");
  check(!calls.some(call => call.url === "/interviews/reload/generate"), "reload repeated generate despite a running C4 state");
  check(eventSources.at(-1).url === "/interviews/reload/generate/events", "reload subscribed to the wrong book stream");
  check(document.querySelector("iframe.race-frame")?.title === "1 of 8 pages finished", "C4 page approval did not restore one real marker");
  check(!document.querySelector(".doors .primary"), "running reload showed the failed-generation door");
}

async function testC4WaitsForOpenAndKeepsBoundaryEvent() {
  results.textContent = "C4 open barrier…";
  let resolveState;
  const state = new Promise(resolve => { resolveState = resolve; });
  await configure({
    id: "barrier",
    status: "ended",
    book_id: "book",
    manualEventSourceOpen: true,
    turns: [{ role: "closing", text: "Goodbye" }],
    responses: { "/book/book/state": state }
  });
  await awaitGenerationRecovery();
  await waitFor(() => eventSources.length >= 2, "reload did not create the book EventSource");
  const source = eventSources.at(-1);
  check(!calls.some(call => call.url === "/book/book/state"), "C4 read started before the book stream opened");
  source.dispatch("page_approved", { n: 4, image_url: "/media/page-4" });
  source.dispatch("open");
  await waitFor(() => calls.some(call => call.url === "/book/book/state"), "C4 read did not start after open");
  resolveState(response({ status: "running", pages: [{ n: 1 }, { n: 2 }, { n: 3 }] }));
  await waitFor(() => document.querySelector("iframe.race-frame")?.title === "4 of 8 pages finished", "C4 snapshot overwrote a boundary stream event");
  check(!source.closed, "running C4 stream closed after the synchronized snapshot");
}

async function testC4StateFailureKeepsLiveStream() {
  results.textContent = "C4 state recovery…";
  await configure({
    id: "state-error",
    status: "ended",
    book_id: "book",
    turns: [{ role: "closing", text: "Goodbye" }],
    responses: { "/book/book/state": response({ error: "internal" }, 500) }
  });
  await awaitGenerationRecovery();
  await waitFor(() => eventSources.length >= 2 && calls.some(call => call.url === "/book/book/state"), "C4 state failure did not reach the synchronized read");
  const source = eventSources.at(-1);
  await waitFor(() => document.querySelector("h1")?.textContent.includes("checking on your book"), "C4 read failure did not render the recoverable state");
  check(!source.closed, "C4 read failure closed a live book stream");
  check(!document.querySelector(".doors .primary"), "C4 read failure showed the failed-generation door");
  source.dispatch("book_ready", { book_id: "book" });
  await settle();
  check(source.closed, "book_ready was ignored after a failed C4 read");
}

async function testC4UnknownStateIsRecoverable() {
  results.textContent = "C4 unknown state recovery…";
  await configure({
    id: "unknown-state",
    status: "ended",
    book_id: "book",
    turns: [{ role: "closing", text: "Goodbye" }],
    responses: { "/book/book/state": response({ status: "unknown", pages: [{ n: 2 }] }) }
  });
  await awaitGenerationRecovery();
  await waitFor(() => eventSources.length >= 2 && calls.some(call => call.url === "/book/book/state"), "unknown C4 state did not reach the synchronized read");
  const source = eventSources.at(-1);
  await waitFor(() => document.querySelector("h1")?.textContent.includes("checking on your book"), "unknown C4 state did not render the recoverable state");
  check(document.querySelector("iframe.race-frame")?.title === "1 of 8 pages finished", "unknown C4 state lost its known approved page");
  check(!source.closed, "unknown C4 state closed the authoritative book stream");
  check(!calls.some(call => call.url === "/interviews/unknown-state/generate"), "unknown C4 state retried generation");
  check(!document.querySelector(".doors .primary"), "unknown C4 state showed the failed-generation door");
  source.dispatch("book_ready", { book_id: "book" });
  await settle();
  check(source.closed, "book_ready was ignored after an unknown C4 state");
}

async function testC4ReconnectRepeatsSnapshot() {
  results.textContent = "C4 reconnect snapshot…";
  let reads = 0;
  await configure({
    id: "reconnect",
    status: "ended",
    book_id: "book",
    turns: [{ role: "closing", text: "Goodbye" }],
    responses: {
      "/book/book/state": () => response(reads++ === 0 ? { status: "running", pages: [] } : { status: "ready", pages: [] })
    }
  });
  await awaitGenerationRecovery();
  await waitFor(() => eventSources.length >= 2 && calls.some(call => call.url === "/book/book/state"), "initial C4 snapshot did not complete");
  const source = eventSources.at(-1);
  check(!source.closed, "running initial C4 stream closed");
  source.dispatch("open");
  await waitFor(() => calls.filter(call => call.url === "/book/book/state").length === 2, "reconnect did not repeat the C4 snapshot");
  await waitFor(() => bookExit === "/book/book", "reconnect snapshot did not recover terminal book state");
}

async function testC4ReconnectQueuesSnapshotDuringInflightRead() {
  results.textContent = "C4 reconnect during snapshot…";
  let reads = 0;
  let resolveInitial;
  const initial = new Promise(resolve => { resolveInitial = resolve; });
  await configure({
    id: "reconnect-inflight",
    status: "ended",
    book_id: "book",
    turns: [{ role: "closing", text: "Goodbye" }],
    responses: {
      "/book/book/state": () => reads++ === 0 ? initial : response({ status: "ready", pages: [] })
    }
  });
  await awaitGenerationRecovery();
  await waitFor(() => calls.filter(call => call.url === "/book/book/state").length === 1, "initial C4 snapshot did not start");
  const source = eventSources.at(-1);
  source.dispatch("error");
  source.drop("book_ready", { book_id: "book" });
  source.dispatch("open");
  resolveInitial(response({ status: "running", pages: [] }));
  await waitFor(() => calls.filter(call => call.url === "/book/book/state").length === 2, "reconnect during the first snapshot did not queue a second read");
  check(source.dropped?.some(event => event.name === "book_ready"), "book_ready gap was not established during disconnect");
  await waitFor(() => bookExit === "/book/book", "post-reconnect snapshot did not recover terminal book state");
}

async function testFreshGenerationSynchronizesAfterOpen() {
  results.textContent = "fresh generation snapshot…";
  await configure({
    id: "fresh",
    status: "ended",
    turns: [{ role: "closing", text: "Goodbye" }],
    responses: { "/book/book/state": response({ status: "running", pages: [{ n: 5 }] }) }
  });
  await skipVoiceSample();
  await waitFor(() => calls.some(call => call.url === "/interviews/fresh/generate"), "fresh generation did not POST");
  await waitFor(() => calls.some(call => call.url === "/book/book/state"), "fresh generation did not synchronize C4 state");
  check(document.querySelector("iframe.race-frame")?.title === "1 of 8 pages finished", "fresh generation snapshot lost its approved page");
}

async function testShelfGestureAndGeneration() {
  results.textContent = "shelf and generation…";
  await configure({ route: "shelf" });
  await waitFor(() => document.querySelector("[data-start]"), "shelf did not render");
  document.querySelector("[data-start]").click();
  check(audioCreated === 1, "shelf CTA did not create exactly one audio element");
  check(location.pathname === "/interview/new", "shelf CTA discarded the same document flow");
  await configure({ status: "ended", turns: [{ role: "closing", text: "Goodbye" }] });
  await reachVoiceStep();
  check(!!document.querySelector("[data-voice-record]") && !!document.querySelector("[data-voice-upload]"), "T13 adult capture controls did not render");
  // The two doors are separate decisions and the cloned one is closed
  // until there is a cloned voice to walk through it.
  check(!document.querySelector("[data-voice-library]").disabled, "the library-voice door was closed");
  check(document.querySelector("[data-voice-continue]").disabled, "the cloned-voice door was open with no recorded voice");
  check(!!document.querySelector("[data-voice-music]"), "the music choice is not its own control");
  check(document.querySelector("[data-voice-consent]")?.checked === false, "voice consent defaults to affirmative");
  check(document.querySelector("[data-voice-record]").disabled, "recording was enabled without adult consent");
  document.querySelector("[data-voice-consent]").click();
  check(document.querySelector("[data-voice-record]").disabled, "recording was enabled without voice-sample authorization");
  const uploadToken = document.querySelector("[data-voice-upload-token]");
  uploadToken.value = "test-upload-token";
  uploadToken.dispatchEvent(new Event("input", { bubbles: true }));
  await settle();
  check(!document.querySelector("[data-voice-record]").disabled, "recording stayed disabled after consent and authorization");
  await skipVoiceSample();
  await waitFor(() => calls.some(call => call.url.endsWith("/generate")), "voice-step skip did not start generation");
  check(document.querySelector("iframe.race-frame")?.src.includes("/static/race/race.html"), "generation screen did not consume the T9a race widget");
  // The wait screen renders the moment generate() is called — before the
  // POST answers and before the book stream is subscribed — so wait for
  // THIS interview's book stream by name rather than taking whichever
  // EventSource happens to be newest.
  const bookEvents = "/interviews/demo/generate/events";
  await waitFor(() => eventSources.some(candidate => candidate.url === bookEvents), "the book stream was never subscribed");
  const source = eventSources.findLast(candidate => candidate.url === bookEvents);
  source.dispatch("page_approved", { n: 8, image_url: "/media/page-8" });
  await waitFor(() => document.querySelector("iframe.race-frame")?.title === "1 of 8 pages finished", "first out-of-order approval did not count as one page");
  source.dispatch("page_approved", { n: 1, image_url: "/media/page-1" });
  await waitFor(() => document.querySelector("iframe.race-frame")?.title === "2 of 8 pages finished", "distinct approvals did not increment the race count");
  const frame = document.querySelector("iframe.race-frame");
  await waitFor(() => frame.contentDocument?.querySelector(".race"), "race iframe did not load for bridge test");
  await waitFor(() => frame.contentDocument.querySelector(".race").style.getPropertyValue("--done") === "2", "parent progress did not reach the T9a iframe bridge");
}

async function testRecorderMimeNegotiation() {
  results.textContent = "recorder MIME negotiation…";
  const supportedCalls = [];
  const created = [];
  class FakeMediaRecorder {
    static isTypeSupported(type) {
      supportedCalls.push(type);
      return type === "audio/mp4";
    }
    constructor(stream, options) {
      this.stream = stream;
      this.mimeType = options.mimeType;
      this.state = "inactive";
      created.push(this);
    }
    start() { this.state = "recording"; this.ondataavailable?.({ data: new Blob(["audio"], { type: this.mimeType }) }); }
    stop() { this.state = "inactive"; this.onstop?.(); }
  }
  const fakeTracks = [{ stop() {} }];
  Object.defineProperty(navigator, "mediaDevices", { configurable: true, value: { getUserMedia: async () => ({ getTracks: () => fakeTracks }) } });
  window.MediaRecorder = FakeMediaRecorder;
  try {
    await configure({ id: "recorder", status: "ended", turns: [{ role: "closing", text: "Goodbye" }], responses: { "/voice-sample": response({ media_url: "https://thutapi.nryn.dev/media/id", source_audio: "https://thutapi.nryn.dev/media/id", voice_id: "provider-voice" }, 201) } });
    await reachVoiceStep();
    document.querySelector("[data-voice-consent]").click();
    const uploadToken = document.querySelector("[data-voice-upload-token]");
    uploadToken.value = "test-upload-token";
    uploadToken.dispatchEvent(new Event("input", { bubbles: true }));
    await settle();
    document.querySelector("[data-voice-record]").click();
    await waitFor(() => created[0]?.state === "recording", "recorder did not start");
    document.querySelector("[data-voice-stop]").click();
    await waitFor(() => calls.some(call => call.url === "/voice-sample"), "recorder did not upload");
    check(supportedCalls.length > 0 && created[0].mimeType === "audio/mp4", "recorder did not choose the supported MIME candidate");
    check(calls.find(call => call.url === "/voice-sample").init.headers["X-Voice-Sample-Consent"] === "yes", "recorder upload omitted consent header");
    check(calls.find(call => call.url === "/voice-sample").init.headers.Authorization === "Bearer test-upload-token", "recorder upload omitted bearer authorization");
    // A verified clone opens the cloned-voice door and sends the voice id
    // on to generation.
    await waitFor(() => !document.querySelector("[data-voice-continue]").disabled, "a verified clone did not open the cloned-voice door");
    document.querySelector("[data-voice-continue]").click();
    await waitFor(() => calls.some(call => call.url.endsWith("/generate")), "the cloned-voice door did not start generation");
    const started = JSON.parse(calls.find(call => call.url.endsWith("/generate")).init.body);
    check(started.voice_id === "provider-voice", "generation was started without the cloned voice id");
  } finally {
    window.MediaRecorder = originalMediaRecorder;
    Object.defineProperty(navigator, "mediaDevices", { configurable: true, value: originalMediaDevices });
  }
}

// A voice service that answers 200 with narrator "library" is a SUCCESS
// with a different narrator: the recording was fine, the clone was not.
// The adult must be told which voice they are getting and still be able to
// walk through the library door — never shown the failure message that used
// to greet a perfectly good recording.
async function testLibraryFallbackIsNotAFailure() {
  results.textContent = "library narrator fallback…";
  const originalMediaDevices = Object.getOwnPropertyDescriptor(navigator, "mediaDevices");
  const originalMediaRecorder = window.MediaRecorder;
  const created = [];
  class FakeMediaRecorder {
    static isTypeSupported() { return true; }
    constructor(stream, options) {
      this.mimeType = options.mimeType;
      this.state = "inactive";
      created.push(this);
    }
    start() { this.state = "recording"; this.ondataavailable?.({ data: new Blob(["audio"], { type: this.mimeType }) }); }
    stop() { this.state = "inactive"; this.onstop?.(); }
  }
  const fakeTracks = [{ stop() {} }];
  Object.defineProperty(navigator, "mediaDevices", { configurable: true, value: { getUserMedia: async () => ({ getTracks: () => fakeTracks }) } });
  window.MediaRecorder = FakeMediaRecorder;
  try {
    await configure({ id: "fallback", status: "ended", turns: [{ role: "closing", text: "Goodbye" }], responses: { "/voice-sample": response({ narrator: "library" }, 200) } });
    await reachVoiceStep();
    document.querySelector("[data-voice-consent]").click();
    const uploadToken = document.querySelector("[data-voice-upload-token]");
    uploadToken.value = "test-upload-token";
    uploadToken.dispatchEvent(new Event("input", { bubbles: true }));
    await settle();
    document.querySelector("[data-voice-record]").click();
    await waitFor(() => created[0]?.state === "recording", "recorder did not start");
    // The countdown is the only feedback while the adult is talking.
    await waitFor(() => document.querySelector("[data-voice-countdown]"), "no countdown while recording");
    check(/12 seconds left/.test(document.querySelector("[data-voice-countdown]").textContent), "the countdown did not start at the full sample length");
    document.querySelector("[data-voice-stop]").click();
    await waitFor(() => calls.some(call => call.url === "/voice-sample"), "recorder did not upload");
    // Match the fallback sentence itself: "library narrator" also appears
    // in the step's own intro copy, so a looser match passes before the
    // upload has even answered.
    await waitFor(() => /couldn\u2019t make a copy of it/.test(document.body.textContent), "the library-narrator fallback was not explained");
    check(!/couldn\u2019t prepare that sample/.test(document.body.textContent), "a good recording was reported as a failed one");
    check(document.querySelector("[data-voice-continue]").disabled, "the cloned-voice door opened with no cloned voice");
    check(!document.querySelector("[data-voice-library]").disabled, "the library door was closed after the fallback");
    document.querySelector("[data-voice-library]").click();
    await waitFor(() => calls.some(call => call.url.endsWith("/generate")), "the library door did not start generation");
    const started = JSON.parse(calls.find(call => call.url.endsWith("/generate")).init.body);
    check(started.voice_id === "", "the library door sent a voice id");
    check(started.music === true, "the library door dropped the music choice");
  } finally {
    window.MediaRecorder = originalMediaRecorder;
    Object.defineProperty(navigator, "mediaDevices", { configurable: true, value: originalMediaDevices });
  }
}

// The wait screen's percentage moves on the run's real stages. Before the
// stage event existed it could only count pages, so a run four minutes into
// narration read the same as one that had just finished drawing.
async function testWaitProgressFollowsTheStages() {
  results.textContent = "wait-screen progress…";
  await configure({ id: "progress", status: "ended", turns: [{ role: "closing", text: "Goodbye" }] });
  await skipVoiceSample();
  await waitFor(() => document.querySelector("[data-book-percent]"), "the wait screen showed no progress reading");
  const percent = () => Number(document.querySelector("[data-book-percent]").dataset.bookPercent);
  // The wait screen renders the moment generate() is called, which is
  // BEFORE the POST answers and before the book stream is subscribed, and
  // a previous test's generation can still be settling in the background.
  // Find THIS interview's book stream by name rather than taking the most
  // recent one, or every dispatch below goes to somebody else's stream.
  const bookEvents = `/interviews/progress/generate/events`;
  await waitFor(() => eventSources.some(source => source.url === bookEvents), "this interview's book stream was never subscribed");
  const source = eventSources.findLast(candidate => candidate.url === bookEvents);

  const percentBecomes = async (predicate, message) => {
    await waitFor(() => predicate(percent()), `${message} (reading stayed at ${percent()}%)`);
    return percent();
  };

  source.dispatch("stage", { stage: "illustrating" });
  const drawing = await percentBecomes(p => p >= 8, "entering the drawing stage did not set a reading");
  for (const n of [1, 2, 3, 4]) source.dispatch("page_approved", { n, image_url: `/media/page-${n}` });
  const halfDrawn = await percentBecomes(p => p > drawing, "approved pages did not move the reading");

  source.dispatch("stage", { stage: "narrating" });
  const narrating = await percentBecomes(p => p > halfDrawn, "entering narration did not move the reading past the drawing band");
  await waitFor(() => /voices/.test(document.querySelector(".wait h1").textContent), "the headline still claimed the animals were drawing");

  source.dispatch("stage", { stage: "filming" });
  const filming = await percentBecomes(p => p > narrating, "entering the film stage did not move the reading");
  check(filming < 100, "the reading reached 100% before the book was ready");

  // The way out of the wait: the run keeps going without the child.
  check(!!document.querySelector("[data-wait-leave]"), "the wait screen offered no way to go and look at other books");
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
    await testEndedReloadCatchesUpRunningGeneration();
    await testC4WaitsForOpenAndKeepsBoundaryEvent();
    await testC4StateFailureKeepsLiveStream();
    await testC4UnknownStateIsRecoverable();
    await testC4ReconnectRepeatsSnapshot();
    await testC4ReconnectQueuesSnapshotDuringInflightRead();
    await testFreshGenerationSynchronizesAfterOpen();
    await testShelfGestureAndGeneration();
    await testRecorderMimeNegotiation();
    await testLibraryFallbackIsNotAFailure();
    await testWaitProgressFollowsTheStages();
    results.innerHTML = "<h1>PASS</h1><pre>opening and terminal catch-up recovery\ncatch-up ordering, chips, and current-turn audio\nshelf unlock, no-sample route, distinct approval progress, and iframe bridge\nvoice doors, recording countdown, library-narrator fallback\nstage-driven progress reading and the way out of the wait</pre>";
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
