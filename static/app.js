import { h, render } from "/static/vendor/preact.module.js";
import htm from "/static/vendor/htm.module.js";
import { useEffect, useRef, useState } from "/static/vendor/hooks.module.js";
import { readBookState, subscribeBook } from "/static/book/catchup.js";

const html = htm.bind(h);

// This is the only Audio object the shell creates. It is made by the shelf
// gesture and survives the interview because the flow stays in this document.
let sharedAudio;
let sharedAudioReady;
let sharedAudioUnlocked = false;
const pendingInterviewStreams = new Map();
const pendingInterviewOpenings = new Map();

// A tiny valid PCM WAV gives the gesture a real, decodable source to play.
// Playing an Audio element with no source does not unlock mobile autoplay.
const silentUnlockURL = "data:audio/wav;base64,UklGRiYAAABXQVZFZm10IBAAAAABAAEAQB8AAIA+AAACABAAZGF0YQIAAAAAAA==";

// EventSource can connect before the interview component has mounted. Keep a
// tiny hand-off buffer for that interval so the opening question's chips are
// delivered to the eventual subscriber instead of being lost during the
// same-document route change.
function subscribeInterview(eventsURL) {
  const source = new EventSource(eventsURL);
  const queued = [];
  let deliver;
  const buffer = (name, event) => {
    if (deliver) deliver(name, event);
    else queued.push({ name, event });
  };
  for (const name of ["question", "question_audio", "ended", "error"]) {
    source.addEventListener(name, event => buffer(name, event));
  }
  return {
    source,
    adopt(handler) {
      deliver = handler;
      // Each queued entry is a {name, event} WRAPPER, so the event handed
      // on is entry.event — not the entry. Passing the wrapper gave the
      // handler an object with no .data, so its JSON.parse threw
      // SyntaxError: "undefined" is not valid JSON, out of a preact effect
      // — which killed the rest of that effect, including the catch-up read
      // that follows it. The child was left on "I'm thinking of a good
      // question…" with a spinner and no way forward, every time the
      // opening question arrived during the route hand-off.
      for (const entry of queued.splice(0)) deliver(entry.name, entry.event);
    }
  };
}

// eventPayload reads one SSE frame's JSON. A frame this app cannot parse is
// ignored rather than thrown out of: these handlers run inside a preact
// effect, and an exception there abandons the REST of that effect — which is
// how one malformed frame used to cost the interview its catch-up read and
// leave the child on "I'm thinking of a good question…" forever. A frame
// that carries nothing readable is not worth a dead screen.
function eventPayload(event) {
  try {
    return JSON.parse(event?.data ?? "null");
  } catch (_) {
    return null;
  }
}

function unlockAudio() {
  if (!sharedAudio) {
    sharedAudio = new Audio();
    sharedAudio.setAttribute("playsinline", "");
  }
  if (sharedAudioUnlocked) return Promise.resolve(true);
  sharedAudio.src = silentUnlockURL;
  sharedAudio.muted = true;
  try {
    sharedAudioReady = Promise.resolve(sharedAudio.play()).then(() => {
      sharedAudio.pause();
      sharedAudio.currentTime = 0;
      sharedAudio.muted = false;
      sharedAudioUnlocked = true;
      return true;
    }, () => false);
  } catch (_) {
    sharedAudioReady = Promise.resolve(false);
  }
  return sharedAudioReady;
}

async function play(url, unlock = false) {
  if (unlock) await unlockAudio();
  if (!sharedAudio || !url) return false;
  if (sharedAudioReady && !(await sharedAudioReady) && !unlock) return false;
  const player = sharedAudio;
  player.src = url;
  try {
    await player.play();
    return true;
  } catch (_) {
    return false;
  }
}

async function json(url, init) {
  const response = await fetch(url, init);
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(body.error || "internal");
    error.kind = body.error || "internal";
    error.status = response.status;
    throw error;
  }
  return body;
}

function readShelfBooks() {
  const el = document.getElementById("shelf-books");
  if (el && el.textContent) {
    try {
      const parsed = JSON.parse(el.textContent);
      if (Array.isArray(parsed)) return parsed;
    } catch (_) {}
  }
  const app = document.querySelector("#app");
  if (app && app.dataset && app.dataset.books) {
    try {
      const parsed = JSON.parse(app.dataset.books);
      if (Array.isArray(parsed)) return parsed;
    } catch (_) {}
  }
  return [];
}

function Shelf({ onStart, books = readShelfBooks() }) {
  return html`<section class="card shelf-card"><p class="eyebrow">Thutapi picture books</p><h1>Stories made from your ideas.</h1><p>Pick a cozy book from the shelf, or make one with a grown-up.</p><a class="primary" href="/interview/new" data-start onClick=${onStart}>Make your own book</a><p class="warm">One tap starts our story.</p>${books && books.length > 0 ? html`<section class="shelf-books"><h2>From the shelf</h2><div class="book-list">${books.map(b => html`<a class="book-card" href="/book/${b.id}"><span class="book-title">${b.title}</span>${b.byline ? html`<span class="book-byline warm">By ${b.byline}</span>` : null}</a>`)}</div></section>` : null}</section>`;
}

function Byline({ onStart }) {
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit(event) {
    event.preventDefault();
    setBusy(true);
    try {
      const start = await json("/interviews", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ byline: name })
      });
      // Subscribe as soon as the 201 exposes the topic. The stream is handed
      // to Interview before its catch-up read, so an opening event that lands
      // during the route transition retains its chips and current turn.
      pendingInterviewStreams.set(start.id, subscribeInterview(start.events_url));
      if (start.opening) pendingInterviewOpenings.set(start.id, start.opening);
      onStart(start);
    } catch (_) {
      setBusy(false);
    }
  }
  return html`<section class="card"><p class="eyebrow">Let’s make a book</p><h1>Whose book are we making?</h1><form onSubmit=${submit}><label>Your name <input value=${name} onInput=${e => setName(e.currentTarget.value)} autoFocus=${true} autocomplete="given-name" /></label><button class="primary" disabled=${busy}>${busy ? "Starting…" : "Start our story"}</button></form><p class="warm">You can leave it blank if you like.</p></section>`;
}

// The T9a widget owns the markup and animation. T9 only supplies its one
// documented input through the bridge, so race fixes remain in static/race.
function Race({ done, sitting }) {
  const frame = useRef(null);
  useEffect(() => {
    frame.current?.contentWindow?.postMessage({ type: "thutapi-race-done", done }, window.location.origin);
  }, [done]);
  function sendProgress(event) {
    event.currentTarget.contentWindow?.postMessage({ type: "thutapi-race-done", done }, window.location.origin);
  }
  return html`<section class=${sitting ? "race-wrap sitting" : "race-wrap"}><iframe ref=${frame} class="race-frame" src="/static/race/race.html?embed=1" title="${done} of 8 pages finished" onLoad=${sendProgress}></iframe></section>`;
}

// leaveForBook is the ONE place the app leaves its own single-page flow for
// the server-rendered /book/{id} page. It is a mutable binding behind a
// setter rather than an inline window.location.assign for one reason:
// location.assign cannot be redefined ("Cannot redefine property: assign"),
// so an inline call leaves book_ready unexercisable — the first one
// navigates the browser-contract page away and the whole run ends silently,
// mid-suite, reporting neither pass nor failure. Production never calls the
// setter and always takes the default.
let leaveForBook = url => window.location.assign(url);

// setBookExit replaces where a finished book sends the reader. It exists for
// static/browser-test.js; passing nothing restores the real navigation.
export function setBookExit(exit) {
  leaveForBook = exit || (url => window.location.assign(url));
}

function cacheKey(id) {
  return `thutapi:question:${id}`;
}

function rememberQuestion(id, question) {
  try {
    sessionStorage.setItem(cacheKey(id), JSON.stringify(question));
  } catch (_) {
    // Storage can be disabled; the live SSE path still works.
  }
}

function recoverQuestion(id, turn, text) {
  try {
    const cached = JSON.parse(sessionStorage.getItem(cacheKey(id)) || "null");
    if (cached && cached.turn === turn && cached.text === text) return cached;
  } catch (_) {
    // Treat unavailable or malformed storage as an empty cache.
  }
  return { turn, text, chips: [], audioURL: "" };
}

// The cloned voice survives a reload and a retry tap in session storage,
// the same place and with the same tolerance for storage being switched off
// as the question cache above: no voice id simply means the library voice.
const voiceKey = "thutapi:voice-id";

function rememberVoiceID(voiceID) {
  try {
    sessionStorage.setItem(voiceKey, voiceID);
  } catch (_) {
    // Storage can be disabled; the book is narrated by the library voice.
  }
}

function savedVoiceID() {
  try {
    return sessionStorage.getItem(voiceKey) || "";
  } catch (_) {
    return "";
  }
}

// STAGE_CEILINGS is how much of the run each stage has finished by the time
// it ENDS, as a fraction. The weights are the shape of a real run: drawing
// eight pages is about half of it, narration is the next largest piece, and
// the PDF and the film close it out. Nothing here ever reaches 1 — the
// hundred belongs to book_ready, which navigates away.
const STAGE_CEILINGS = { structuring: 0.08, illustrating: 0.58, narrating: 0.85, binding: 0.9, filming: 0.99 };
const STAGE_FLOORS = { structuring: 0, illustrating: 0.08, narrating: 0.58, binding: 0.85, filming: 0.9 };

// progressPercent turns the run's real signals into a whole-number reading.
// Only the illustrating stage has per-page progress, so it interpolates
// across its band on the approved count; every other stage reports its own
// floor, which is a true statement — "at least this much is finished" — and
// moves the moment the next stage event lands. The pre-stage reading falls
// back to the page count alone so a client that reconnects to a run whose
// stage event it missed still shows something honest.
function progressPercent(stage, done) {
  const pages = Math.max(0, Math.min(8, done)) / 8;
  if (!stage) return Math.round(STAGE_FLOORS.illustrating * 100 + (STAGE_CEILINGS.illustrating - STAGE_FLOORS.illustrating) * pages * 100);
  const floor = STAGE_FLOORS[stage];
  const ceiling = STAGE_CEILINGS[stage];
  if (floor === undefined) return 0;
  if (stage !== "illustrating") return Math.round(floor * 100);
  return Math.round((floor + (ceiling - floor) * pages) * 100);
}

// waitHeadline says what the animals are doing right now. It reads the run's
// real stage rather than inferring one from the page count, which is how a
// run four minutes into narration used to still say it was drawing.
function waitHeadline(bookState, stage, done) {
  if (bookState === "failed") return "The animals need a little rest.";
  if (bookState === "checking") return "We’re checking on your book.";
  if (stage === "narrating") return "The animals are giving your characters voices.";
  if (stage === "binding") return "The animals are folding your pages together.";
  if (stage === "filming") return "The animals are making your story film.";
  if (stage === "structuring") return "The animals are thinking up your pages.";
  if (stage === "illustrating") return "The animals are drawing your story.";
  return done === 8 ? "The animals are giving your characters voices." : "The animals are drawing your story.";
}

function warmError(kind) {
  if (kind === "not_found") return "That story wandered away. Start a new one below.";
  return "That question took a tiny tumble. Share your idea again and we’ll keep going.";
}

// RECORDING_SECONDS is how long a sample records before it stops itself. The
// server caps a decoded sample at 15 seconds, so this leaves headroom for a
// browser that mixes a slightly longer container than it was asked for.
const RECORDING_SECONDS = 12;

// uploadNotice turns a voice-sample response into the one sentence the adult
// reads. Every failure used to arrive as the same "we couldn't prepare that
// sample", which said "your recording was bad" for a bad access code, a long
// file, and a voice service that simply had nothing to give back — three
// problems with three different things to do about them (Live bug report 1).
function uploadNotice(status) {
  if (status === 401 || status === 403) return "That voice-sample access code wasn’t right. Check it with the grown-up who set this up and try again.";
  if (status === 413) return "That sample is a little too long. About 8–15 seconds is perfect.";
  if (status === 415) return "We couldn’t read that file. Choose a webm, mp4, mp3, or wav sample.";
  if (status === 429) return "The voice service is busy right now. Wait a moment and try again, or continue with our library narrator.";
  if (status >= 500) return "The voice service isn’t answering just now. You can try again, or continue with our warm library narrator.";
  return "We couldn’t prepare that sample. Try recording again or choose a webm, mp4, mp3, or wav file.";
}

function VoiceSample({ onContinue }) {
  const [state, setState] = useState("ready");
  const [notice, setNotice] = useState("");
  const [sampleURL, setSampleURL] = useState("");
  const [voiceID, setVoiceID] = useState("");
  const [consent, setConsent] = useState(false);
  const [uploadToken, setUploadToken] = useState("");
  const [music, setMusic] = useState(true);
  const [countdown, setCountdown] = useState(0);
  const recorder = useRef(null);
  const stream = useRef(null);
  const ticker = useRef(null);
  const timer = useRef(null);

  function stopTracks() {
    stream.current?.getTracks().forEach(track => track.stop());
    stream.current = null;
  }

  function stopCountdown() {
    clearInterval(ticker.current);
    ticker.current = null;
    setCountdown(0);
  }

  async function upload(blob) {
    if (!blob || !blob.size) throw new Error("empty sample");
    if (blob.size > 5 * 1024 * 1024) throw new Error("too large");
    if (!consent) throw new Error("consent required");
    if (!uploadToken.trim()) throw new Error("upload authorization required");
    setState("uploading");
    setNotice("Preparing your short sample…");
    const form = new FormData();
    form.append("sample", blob, "voice-sample" + (blob.type.includes("mp4") ? ".mp4" : ".webm"));
    const response = await fetch("/voice-sample", { method: "POST", headers: { "X-Voice-Sample-Consent": "yes", "Authorization": "Bearer " + uploadToken.trim() }, body: form });
    if (!response.ok) {
      const failure = new Error("upload failed");
      failure.status = response.status;
      throw failure;
    }
    const saved = await response.json();
    // The server keeps the recording whatever the voice service does, so a
    // response with no cloned voice is a SUCCESS with a different narrator —
    // not a failure to hand back to the adult as one. Either way this step
    // is finished and the Continue door opens.
    setState("saved");
    if (saved.narrator === "library" || !saved.voice_id) {
      setSampleURL("");
      setVoiceID("");
      setNotice("We recorded that beautifully, but the voice service couldn’t make a copy of it this time. Your book will be read in our warm library narrator instead.");
      return;
    }
    setSampleURL(saved.source_audio || "");
    setVoiceID(saved.voice_id);
    setNotice("That voice is ready — your book will be read in it.");
  }

  async function startRecording() {
    // getUserMedia is deliberately called only from this button's gesture.
    setNotice("");
    if (window.isSecureContext === false) {
      setNotice("Microphone recording requires a secure HTTPS connection or localhost. Please choose a sample file below or open via HTTPS.");
      return;
    }
    try {
      if (!consent) throw new Error("consent required");
      if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === "undefined") throw new Error("recorder unavailable");
      const mic = await navigator.mediaDevices.getUserMedia({ audio: true });
      stream.current = mic;
      const candidates = ["audio/webm;codecs=opus", "audio/webm", "audio/mp4;codecs=mp4a.40.2", "audio/mp4"];
      const supported = typeof MediaRecorder.isTypeSupported === "function" ? candidates.find(type => MediaRecorder.isTypeSupported(type)) : "";
      if (!supported) {
        stopTracks();
        throw new Error("recorder unavailable");
      }
      const next = new MediaRecorder(mic, { mimeType: supported });
      const chunks = [];
      next.ondataavailable = event => { if (event.data.size) chunks.push(event.data); };
      next.onerror = () => {
        stopTracks();
        stopCountdown();
        setState("ready");
        setNotice("The microphone recording stopped early. You can try again or choose a file.");
      };
      next.onstop = async () => {
        clearTimeout(timer.current);
        stopTracks();
        stopCountdown();
        try {
          await upload(new Blob(chunks, { type: next.mimeType || "audio/webm" }));
        } catch (error) {
          setState("ready");
          setNotice(error.message === "too large" ? "That recording is too large. Please keep it to a short 8–15 second sample." : uploadNotice(error.status || 0));
        }
      };
      recorder.current = next;
      next.start();
      setState("recording");
      setNotice("");
      // The countdown is the whole feedback for this step: the adult is
      // talking at a screen that otherwise does not move, and has no other
      // way to know how much of the sample is left.
      setCountdown(RECORDING_SECONDS);
      clearInterval(ticker.current);
      ticker.current = setInterval(() => setCountdown(left => (left > 0 ? left - 1 : 0)), 1000);
      timer.current = setTimeout(() => { if (next.state === "recording") next.stop(); }, RECORDING_SECONDS * 1000);
    } catch (error) {
      stopCountdown();
      setState("ready");
      setNotice(
        error.name === "NotAllowedError"
          ? "Microphone permission is needed to record. You can choose a sample file instead."
          : error.message === "consent required"
          ? "An adult must agree to the voice-sample information first."
          : error.message === "upload authorization required"
          ? "Enter the adult voice-sample access code before recording."
          : window.isSecureContext === false
          ? "Microphone recording requires a secure HTTPS connection or localhost. Please choose a sample file below or open via HTTPS."
          : "We couldn’t open the microphone in this browser. You can choose a prepared sample file instead."
      );
    }
  }

  function stopRecording() {
    if (recorder.current?.state === "recording") recorder.current.stop();
  }

  async function chooseFile(event) {
    const file = event.currentTarget.files?.[0];
    if (!file) return;
    setNotice("");
    if (!consent) {
      setNotice("An adult must agree to the voice-sample information before uploading.");
      event.currentTarget.value = "";
      return;
    }
    try {
      await upload(file);
    } catch (error) {
      setState("ready");
      setNotice(error.message === "too large" ? "That file is too large. Please choose a short 8–15 second sample." : uploadNotice(error.status || 0));
    } finally {
      event.currentTarget.value = "";
    }
  }

  useEffect(() => () => { clearTimeout(timer.current); clearInterval(ticker.current); if (recorder.current?.state === "recording") recorder.current.stop(); stopTracks(); }, []);

  // Both doors carry the music choice. The old single "Skip for now" made
  // one decision look like three: it read as skipping the music too, and it
  // was the only way forward when a clone did not come back, so declining a
  // voice and losing a voice were the same button. Now the music checkbox is
  // its own choice above them, and the two doors differ only in which voice
  // reads the book — with the cloned one enabled solely when there is a
  // cloned voice to offer.
  const busy = state === "recording" || state === "uploading";
  const blocked = !consent || !uploadToken.trim() || busy;
  return html`<section class="card voice-sample"><p class="eyebrow">Grown-up corner</p><h1>Would you like to add a short voice sample?</h1><p>With adult permission, record about 8–15 seconds or choose a prepared file. This is optional; your book can use our warm library narrator.</p><label class="consent"><input data-voice-consent type="checkbox" checked=${consent} onChange=${event => setConsent(event.currentTarget.checked)} /> I’m an adult and I understand this sample is uploaded to this app’s public media URL so GMI can fetch it for voice cloning. The app deletes it after 15 minutes and it is never shared-cacheable. GMI may return generated voice audio at a public provider URL that we cannot delete.</label><label>Voice-sample access code<input data-voice-upload-token type="password" value=${uploadToken} onInput=${event => setUploadToken(event.currentTarget.value)} autocomplete="one-time-code" /></label><div class="doors"><button class="primary" data-voice-record onClick=${startRecording} disabled=${blocked}>${state === "recording" ? `Recording… ${countdown}s` : "Record a sample"}</button><button class="secondary" data-voice-stop onClick=${stopRecording} disabled=${state !== "recording"}>Stop recording</button></div>${state === "recording" ? html`<p class="warm countdown" role="status" data-voice-countdown>Recording — ${countdown} second${countdown === 1 ? "" : "s"} left. Say something warm and ordinary, like a line from a favourite book.</p>` : null}<label class="voice-upload">Choose a sample file<input data-voice-upload type="file" accept="audio/webm,video/webm,audio/mp4,video/mp4,audio/mpeg,audio/wav,.webm,.mp4,.m4a,.mp3,.wav" onChange=${chooseFile} disabled=${blocked} /></label><label class="music-option"><input data-voice-music type="checkbox" checked=${music} onChange=${e => setMusic(e.currentTarget.checked)} /> Add gentle background music to my story film</label>${notice ? html`<p class="warm" role="status">${notice}</p>` : null}${sampleURL ? html`<p class="warm">Your sample stays only while the voice service reads it, then it is deleted.</p>` : null}<div class="doors"><button class="secondary" data-voice-library onClick=${() => onContinue(undefined, music)} disabled=${busy}>Continue with our library voice</button><button class="primary" data-voice-continue onClick=${() => onContinue(voiceID, music)} disabled=${busy || !voiceID}>Continue with your voice</button></div>${voiceID ? null : html`<p class="warm">“Continue with your voice” opens once a sample has been recorded and the voice service has copied it.</p>`}</section>`;
}

function isFarewell(text) {
  if (!text) return false;
  const lower = text.trim().toLowerCase();
  // A farewell inside a question remains answerable, e.g. "What does Pip
  // say when waving goodbye?". Only declarative closing text hides the form.
  if (lower.includes("?")) return false;
  const withoutFinalPunctuation = lower.replace(/[.! \t\r\n]+$/, "");
  const lastPunctuation = Math.max(
    withoutFinalPunctuation.lastIndexOf("."),
    withoutFinalPunctuation.lastIndexOf("!")
  );
  const lastSentence = withoutFinalPunctuation.slice(lastPunctuation + 1).trim();
  if (["goodbye", "good-bye", "bye", "farewell"].includes(lastSentence)) return true;
  return [
    "make your book",
    "make our book",
    "make the book",
    "draw your book",
    "drawing your book",
    "create your book",
    "ready to make your book",
    "ready for your book",
    "go make your book",
    "go make our book",
    "go make the book",
    "make your book now",
    "make our book now",
    "make the book now"
  ].some(ending => lastSentence.endsWith(ending));
}

function Interview({ id }) {
  const [question, setQuestion] = useState(null);
  const [answer, setAnswer] = useState("");
  const [waiting, setWaiting] = useState(true);
  const [ended, setEnded] = useState(false);
  const [isClosing, setIsClosing] = useState(false);
  const [audioURL, setAudioURL] = useState("");
  const [needsTap, setNeedsTap] = useState(false);
  const [notice, setNotice] = useState("");
  const [done, setDone] = useState(0);
  const [stage, setStage] = useState("");
  const [bookState, setBookState] = useState("");
  const stream = useRef(null);
  const bookStream = useRef(null);
  const activeTurn = useRef(0);
  const seenTurns = useRef(new Set());
  const approvedPages = useRef(new Set());
  const generationStarted = useRef(false);
  const bookID = useRef("");
  const [missing, setMissing] = useState(false);

  function listen(url, unlock = false) {
    play(url, unlock).then(ok => setNeedsTap(!ok));
  }

  function applyQuestion(q) {
    if (!q || !q.turn || seenTurns.current.has(q.turn)) return;
    seenTurns.current.add(q.turn);
    activeTurn.current = Math.max(activeTurn.current, q.turn);
    const next = { turn: q.turn, text: q.text, chips: q.chips || [], audioURL: q.audio_url || "" };
    setQuestion(next);
    setAudioURL(next.audioURL);
    setNeedsTap(false);
    setNotice("");
    setWaiting(false);
    rememberQuestion(id, next);
  }

  function applyRecoveredQuestion(q) {
    // A same-turn SSE question is authoritative: it carries live chips that
    // the transcript deliberately does not persist. Catch-up only fills a
    // turn the live subscription has not already supplied.
    if (!q || !q.turn || q.turn <= activeTurn.current) return;
    activeTurn.current = q.turn;
    const next = { turn: q.turn, text: q.text, chips: q.chips || [], audioURL: q.audioURL || q.audio_url || "" };
    setQuestion(next);
    setAudioURL(next.audioURL);
    setNeedsTap(false);
    setWaiting(false);
    rememberQuestion(id, next);
  }

  function openInterview(eventsURL) {
    stream.current && stream.current.close();
    const handoff = pendingInterviewStreams.get(id);
    pendingInterviewStreams.delete(id);
    const subscription = handoff || subscribeInterview(eventsURL);
    const source = subscription.source;
    stream.current = source;
    subscription.adopt((name, event) => {
      const payload = eventPayload(event);
      if (name === "question") {
        if (payload) applyQuestion(payload);
        return;
      }
      if (name === "question_audio") {
        const audio = payload;
        if (!audio || audio.turn !== activeTurn.current || !audio.audio_url) return;
        setAudioURL(audio.audio_url);
        setQuestion(current => {
          if (!current || current.turn !== audio.turn) return current;
          const next = { ...current, audioURL: audio.audio_url };
          rememberQuestion(id, next);
          return next;
        });
        listen(audio.audio_url);
        return;
      }
      if (name === "ended") {
        const end = payload || {};
        setIsClosing(true);
        setWaiting(false);
        setQuestion({ turn: activeTurn.current, text: end.text || "What a lovely story! Let's make your book.", chips: [], audioURL: "" });
        source.close();
        return;
      }
      const failure = payload || {};
      if (failure.error === "not_found") {
        source.close();
        setMissing(true);
      }
      setNotice(warmError(failure.error));
      setWaiting(false);
    });
  }

  function attachBookStream(eventsURL, currentBookID) {
    bookStream.current && bookStream.current.close();
    const subscription = subscribeBook(eventsURL, {
      reconnected: epoch => {
        synchronizeBookState(subscription, currentBookID, false, epoch);
      },
      pageApproved: pageEvent => {
        const page = pageEvent.n;
        if (!Number.isInteger(page) || page < 1 || page > 8 || approvedPages.current.has(page)) return;
        approvedPages.current.add(page);
        setDone(approvedPages.current.size);
        setBookState("drawing");
      },
      stage: stageEvent => {
        if (STAGE_FLOORS[stageEvent.stage] !== undefined) setStage(stageEvent.stage);
      },
      narrationUnavailable: () => setBookState("quiet"),
      bookReady: ready => {
        leaveForBook(`/book/${currentBookID || ready.book_id || ""}`);
      },
      failed: () => {
        setBookState("failed");
      }
    });
    bookStream.current = subscription;
    // EventSource reconnects itself after a transient network error. Keep it
    // open until the terminal event so a reload can receive live progress.
    return subscription;
  }

  const stateReadTrackers = new WeakMap();
  async function synchronizeBookState(subscription, currentBookID, startIfAbsent, requestedEpoch = 0) {
    if (subscription.isTerminal()) return;
    let tracker = stateReadTrackers.get(subscription);
    if (!tracker) {
      tracker = { running: false, requestedEpoch: 0 };
      stateReadTrackers.set(subscription, tracker);
    }
    tracker.requestedEpoch = Math.max(tracker.requestedEpoch, requestedEpoch);
    if (tracker.running) return;
    tracker.running = true;
    try {
      for (;;) {
        const result = await readBookState(subscription, () => json(`/book/${currentBookID}/state`));
        if (result.terminal || subscription.isTerminal()) return;
        const readEpoch = result.epoch;
        if (result.error) {
          if (tracker.requestedEpoch > readEpoch || subscription.connectionEpoch() > readEpoch) continue;
          setBookState("checking");
        } else {
          // A newer connection owns the authoritative from-now-on stream. Do
          // not let a snapshot from the prior connection overwrite it.
          if (tracker.requestedEpoch > readEpoch || subscription.connectionEpoch() > readEpoch) continue;
          if (applyBookState(result.state, subscription)) return;
          if (startIfAbsent) {
            subscription.close();
            await generate();
            return;
          }
        }
        if (tracker.requestedEpoch <= readEpoch && subscription.connectionEpoch() <= readEpoch) return;
      }
    } finally {
      tracker.running = false;
    }
  }

  function applyBookState(state, subscription) {
    if (subscription.isTerminal()) return true;
    const pages = Array.isArray(state.pages) ? state.pages : [];
    for (const page of pages) {
      if (Number.isInteger(page.n) && page.n >= 1 && page.n <= 8) approvedPages.current.add(page.n);
    }
    setDone(approvedPages.current.size);
    if (STAGE_FLOORS[state.stage] !== undefined) setStage(state.stage);
    if (state.status === "ready") {
      subscription.close();
      leaveForBook(`/book/${bookID.current}`);
      return true;
    }
    if (state.status === "failed") {
      subscription.close();
      setBookState("failed");
      return true;
    }
    if (state.status === "running") {
      setBookState("drawing");
      return true;
    }
    if (state.status === "unknown") {
      // The latest run exists but its job result is unreadable. Keep the
      // authoritative stream alive and wait for a terminal event; posting a
      // new run could spend money while the existing run is still active.
      setBookState("checking");
      return true;
    }
    return false;
  }

  async function recoverGeneration(currentBookID) {
    bookID.current = currentBookID;
    setBookState("drawing");
    setDone(0);
    setStage("");
    approvedPages.current = new Set();
    const subscription = attachBookStream(`/interviews/${id}/generate/events`, currentBookID);
    // C4 is from-now-on: wait until the server has accepted the subscription,
    // then read state. Reconnects repeat this synchronization in the adapter.
    await synchronizeBookState(subscription, currentBookID, true);
  }

  useEffect(() => {
    if (id === "new") return undefined;
    openInterview(`/interviews/${id}/events`);
    const opening = pendingInterviewOpenings.get(id);
    pendingInterviewOpenings.delete(id);
    if (opening) applyRecoveredQuestion(opening);
    json(`/interviews/${id}`).then(state => {
      const turns = state.turns || [];
      const lastIndex = turns.length - 1;
      const last = turns[lastIndex];
      if (state.current) {
        applyRecoveredQuestion(state.current);
      } else if (last && (last.role === "interviewer" || last.role === "closing")) {
        const recovered = recoverQuestion(id, lastIndex + 1, last.text);
        applyRecoveredQuestion(recovered);
      }
      if (state.status === "ended" || (last && last.role === "closing")) {
        // Catch-up can discover the terminal state before the stream's ended
        // event arrives. End the interview subscription before generation
        // opens its separate book stream.
        stream.current?.close();
        if (state.book_id) {
          setEnded(true);
          generationStarted.current = true;
          recoverGeneration(state.book_id);
        } else {
          setIsClosing(true);
          setWaiting(false);
          setQuestion(current => current || { turn: activeTurn.current, text: "What a lovely story! Let's make your book.", chips: [], audioURL: "" });
        }
      }
      if (state.error) {
        const isMissing = state.error === "not_found";
        if (isMissing) stream.current?.close();
        setMissing(isMissing);
        setNotice(warmError(state.error));
        setWaiting(false);
      } else if (!last || last.role === "child") {
        setWaiting(true);
      }
    }).catch(error => {
      const isMissing = error.kind === "not_found";
      if (isMissing) stream.current?.close();
      setMissing(isMissing);
      setNotice(error.kind === "not_found" ? warmError(error.kind) : "We couldn’t read that little step yet. Try your idea once more.");
      setWaiting(false);
    });
    return () => stream.current && stream.current.close();
  }, [id]);

  async function send(text) {
    if (!text.trim() || waiting || isClosing) return;
    setAnswer("");
    setNotice("");
    setWaiting(true);
    try {
      await json(`/interviews/${id}/answers`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text })
      });
    } catch (_) {
      setWaiting(false);
      setNotice("That idea needs one more little try. You can tap a choice or write it again.");
    }
  }

  async function generate(music = true) {
    const musicOption = typeof music === "boolean" ? music : true;
    setBookState("drawing");
    setDone(0);
    setStage("");
    approvedPages.current = new Set();
    try {
      const start = await json(`/interviews/${id}/generate`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        // The cloned voice the adult earned on the voice step. A retry tap
        // reads it back out of session storage, so the second run is read in
        // the same voice as the first.
        body: JSON.stringify({ music: musicOption, voice_id: savedVoiceID() })
      });
      bookID.current = start.book_id || bookID.current;
      const subscription = attachBookStream(start.events_url, bookID.current);
      await synchronizeBookState(subscription, bookID.current, false);
    } catch (error) {
      // A run can begin between the catch-up snapshot and this POST. Busy is
      // therefore a live run to recover, never a failed generation.
      if (error.status === 409 && error.kind === "busy" && bookID.current) {
        await recoverGeneration(bookID.current);
        return;
      }
      setBookState("failed");
    }
  }

  function continueToGeneration(voiceID, music = true) {
    if (generationStarted.current) return;
    rememberVoiceID(voiceID || "");
    generationStarted.current = true;
    const musicOption = typeof music === "boolean" ? music : true;
    generate(musicOption);
  }

  if (bookState) return html`<section class="card wait"><p class="eyebrow">Your book is on its way</p><h1>${waitHeadline(bookState, stage, done)}</h1>${bookState === "failed" ? null : html`<p class="progress" role="status" data-book-percent=${progressPercent(stage, done)}><span class="progress-figure">${progressPercent(stage, done)}%</span> of your book is made</p>`}<${Race} done=${done} sitting=${bookState === "failed"} />${bookState === "checking" ? html`<p class="warm">We’re still listening for the next page.</p>` : null}${bookState === "quiet" ? html`<p class="warm">Your book will be beautifully captioned and quiet today.</p>` : null}${bookState === "failed" ? html`<div class="doors"><button class="primary" onClick=${() => generate(true)}>Try again</button><a class="secondary" href="/">Look at other books</a></div>` : html`<div class="wait-away"><p class="warm">Making a whole book takes a few minutes. The animals keep working even if you go — you can read another book from the shelf and come back.</p><div class="doors"><a class="secondary" data-wait-leave href="/">Look at other books</a></div></div>`}</section>`;
  if (missing) return html`<section class="card"><p class="eyebrow">A tiny detour</p><h1>That story wandered away.</h1><p class="warm" role="status">Start a new story and we’ll make a fresh little path together.</p><div class="doors"><a class="primary" href="/">Start a new story</a><a class="secondary" href="/">Look at other books</a></div></section>`;
  if (ended) return html`<${VoiceSample} onContinue=${continueToGeneration} />`;
  const closing = isClosing || (question && isFarewell(question.text));
  return html`<section class="interview"><p class="eyebrow">Your story</p><div class="question"><h1>${question ? question.text : notice ? "A little hiccup." : "I’m thinking of a good question…"}</h1>${audioURL ? html`<button class="speaker" onClick=${() => listen(audioURL, true)}>${needsTap ? "Tap to listen" : "Listen again"}</button>` : null}</div>${closing ? html`<div class="doors"><button class="primary" data-make-book onClick=${() => setEnded(true)}>Make my book</button></div>` : waiting ? html`<p class="warm">🐇 A little animal is thinking…</p>` : html`<div class="chips">${(question && question.chips || []).map(chip => html`<button onClick=${() => send(chip)}>${chip}</button>`)}</div>`}${notice ? html`<p class="warm" role="status">${notice}</p>` : null}${closing ? null : html`<form class="answer" onSubmit=${event => { event.preventDefault(); send(answer); }}><input value=${answer} onInput=${e => setAnswer(e.currentTarget.value)} onFocus=${e => e.currentTarget.scrollIntoView({ block: "center" })} placeholder="Or write your own idea" autocomplete="off" /><button class="primary" disabled=${waiting}>Send</button></form>`}</section>`;
}

function App({ initialRoute }) {
  const [route, setRoute] = useState(initialRoute);
  function navigate(path, nextRoute) {
    window.history.pushState({}, "", path);
    setRoute(nextRoute);
  }
  if (route === "shelf") {
    return html`<${Shelf} onStart=${event => { event.preventDefault(); unlockAudio(); navigate("/interview/new", "new"); }} />`;
  }
  if (route === "new") return html`<${Byline} onStart=${start => navigate(`/interview/${start.id}`, start.id)} />`;
  return html`<${Interview} id=${route} />`;
}

const root = document.querySelector("#app");
// #app arrives pre-filled with the server's no-JS fallback markup. render()
// appends into a parent it does not own, so drop those children first or the
// fallback headline, CTA and "Preparing your story…" stay on the page beside
// the mounted app. hydrate() is wrong here: the client tree adds a .card /
// .interview wrapper the server markup has no counterpart for.
if (root) root.replaceChildren();
if (root) render(html`<${App} initialRoute=${root.dataset.interviewId || "shelf"} />`, root);
