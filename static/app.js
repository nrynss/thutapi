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
      for (const event of queued.splice(0)) deliver(event.name, event);
    }
  };
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

function warmError(kind) {
  if (kind === "not_found") return "That story wandered away. Start a new one below.";
  return "That question took a tiny tumble. Share your idea again and we’ll keep going.";
}

function VoiceSample({ onContinue }) {
  const [state, setState] = useState("ready");
  const [notice, setNotice] = useState("");
  const [sampleURL, setSampleURL] = useState("");
  const [voiceID, setVoiceID] = useState("");
  const [consent, setConsent] = useState(false);
  const [uploadToken, setUploadToken] = useState("");
  const recorder = useRef(null);
  const stream = useRef(null);
  const timer = useRef(null);

  function stopTracks() {
    stream.current?.getTracks().forEach(track => track.stop());
    stream.current = null;
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
    if (!response.ok) throw new Error("upload failed");
    const saved = await response.json();
    if (!saved.media_url || !saved.source_audio || !saved.voice_id) throw new Error("voice service did not verify a voice id");
    setSampleURL(saved.source_audio);
    setVoiceID(saved.voice_id);
    setState("saved");
    setNotice("Your sample is ready as clone input. The library narrator stays selected until the adult completes the live GMI voice-clone check.");
  }

  async function startRecording() {
    // getUserMedia is deliberately called only from this button's gesture.
    setNotice("");
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
        setState("ready");
        setNotice("The microphone recording stopped early. You can try again or choose a file.");
      };
      next.onstop = async () => {
        clearTimeout(timer.current);
        stopTracks();
        try {
          await upload(new Blob(chunks, { type: next.mimeType || "audio/webm" }));
        } catch (error) {
          setState("ready");
          setNotice(error.message === "too large" ? "That recording is too large. Please keep it to a short 8–15 second sample." : "We couldn’t prepare that sample. Try recording again or choose a webm, mp4, mp3, or wav file.");
        }
      };
      recorder.current = next;
      next.start();
      setState("recording");
      setNotice("Recording now. We’ll stop after 12 seconds.");
      timer.current = setTimeout(() => { if (next.state === "recording") next.stop(); }, 12000);
    } catch (error) {
      setState("ready");
      setNotice(error.name === "NotAllowedError" ? "Microphone permission is needed to record. You can choose a sample file instead." : error.message === "consent required" ? "An adult must agree to the voice-sample information first." : error.message === "upload authorization required" ? "Enter the adult voice-sample access code before recording." : "We couldn’t open the microphone in this browser. You can choose a prepared sample file instead.");
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
      setNotice(error.message === "too large" ? "That file is too large. Please choose a short 8–15 second sample." : "We couldn’t prepare that file. Choose a webm, mp4, mp3, or wav sample.");
    } finally {
      event.currentTarget.value = "";
    }
  }

  useEffect(() => () => { clearTimeout(timer.current); if (recorder.current?.state === "recording") recorder.current.stop(); stopTracks(); }, []);

  return html`<section class="card voice-sample"><p class="eyebrow">Grown-up corner</p><h1>Would you like to add a short voice sample?</h1><p>With adult permission, record about 8–15 seconds or choose a prepared file. This is optional; your book can use our warm library narrator.</p><label class="consent"><input data-voice-consent type="checkbox" checked=${consent} onChange=${event => setConsent(event.currentTarget.checked)} /> I’m an adult and I understand this sample is uploaded to this app’s public media URL so GMI can fetch it for voice cloning. The app deletes it after 15 minutes and it is never shared-cacheable. GMI may return generated voice audio at a public provider URL that we cannot delete.</label><label>Voice-sample access code<input data-voice-upload-token type="password" value=${uploadToken} onInput=${event => setUploadToken(event.currentTarget.value)} autocomplete="one-time-code" /></label><div class="doors"><button class="primary" data-voice-record onClick=${startRecording} disabled=${!consent || !uploadToken.trim() || state === "recording" || state === "uploading"}>${state === "recording" ? "Recording…" : "Record a sample"}</button><button class="secondary" data-voice-stop onClick=${stopRecording} disabled=${state !== "recording"}>Stop recording</button></div><label class="voice-upload">Choose a sample file<input data-voice-upload type="file" accept="audio/webm,video/webm,audio/mp4,video/mp4,audio/mpeg,audio/wav,.webm,.mp4,.m4a,.mp3,.wav" onChange=${chooseFile} disabled=${!consent || !uploadToken.trim() || state === "recording" || state === "uploading"} /></label>${notice ? html`<p class="warm" role="status">${notice}</p>` : null}${sampleURL ? html`<p class="warm">Sample is ready briefly while the voice service verifies it.</p>` : null}<div class="doors"><button class="secondary" data-voice-skip onClick=${() => onContinue()} disabled=${state === "recording" || state === "uploading"}>Skip for now</button>${sampleURL ? html`<button class="primary" data-voice-continue onClick=${() => onContinue(voiceID)}>Continue to my book</button>` : null}</div></section>`;
}

function Interview({ id }) {
  const [question, setQuestion] = useState(null);
  const [answer, setAnswer] = useState("");
  const [waiting, setWaiting] = useState(true);
  const [ended, setEnded] = useState(false);
  const [audioURL, setAudioURL] = useState("");
  const [needsTap, setNeedsTap] = useState(false);
  const [notice, setNotice] = useState("");
  const [done, setDone] = useState(0);
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
      if (name === "question") {
        applyQuestion(JSON.parse(event.data));
        return;
      }
      if (name === "question_audio") {
        const audio = JSON.parse(event.data);
        if (audio.turn !== activeTurn.current || !audio.audio_url) return;
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
        const end = JSON.parse(event.data);
        setEnded(true);
        setWaiting(false);
        setQuestion({ turn: activeTurn.current, text: end.text, chips: [], audioURL: "" });
        source.close();
        return;
      }
      const payload = event.data ? JSON.parse(event.data) : {};
      if (payload.error === "not_found") {
        source.close();
        setMissing(true);
      }
      setNotice(warmError(payload.error));
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
      narrationUnavailable: () => setBookState("quiet"),
      bookReady: ready => {
        window.location.assign(`/book/${currentBookID || ready.book_id || ""}`);
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
    if (state.status === "ready") {
      subscription.close();
      window.location.assign(`/book/${bookID.current}`);
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
        setEnded(true);
        if (state.book_id) {
          generationStarted.current = true;
          recoverGeneration(state.book_id);
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
    if (!text.trim() || waiting) return;
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

  async function generate() {
    setBookState("drawing");
    setDone(0);
    approvedPages.current = new Set();
    try {
      const start = await json(`/interviews/${id}/generate`, { method: "POST" });
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

  function continueToGeneration(voiceID) {
    if (generationStarted.current) return;
    if (voiceID) sessionStorage.setItem("thutapi:voice-id", voiceID);
    generationStarted.current = true;
    generate();
  }

  if (bookState) return html`<section class="card wait"><p class="eyebrow">Your book is on its way</p><h1>${bookState === "failed" ? "The animals need a little rest." : bookState === "checking" ? "We’re checking on your book." : "The animals are drawing your story."}</h1><${Race} done=${done} sitting=${bookState === "failed"} />${bookState === "checking" ? html`<p class="warm">We’re still listening for the next page.</p>` : null}${bookState === "quiet" ? html`<p class="warm">Your book will be beautifully captioned and quiet today.</p>` : null}${bookState === "failed" ? html`<div class="doors"><button class="primary" onClick=${generate}>Try again</button><a class="secondary" href="/">Look at other books</a></div>` : null}</section>`;
  if (missing) return html`<section class="card"><p class="eyebrow">A tiny detour</p><h1>That story wandered away.</h1><p class="warm" role="status">Start a new story and we’ll make a fresh little path together.</p><div class="doors"><a class="primary" href="/">Start a new story</a><a class="secondary" href="/">Look at other books</a></div></section>`;
  if (ended) return html`<${VoiceSample} onContinue=${continueToGeneration} />`;
  return html`<section class="interview"><p class="eyebrow">Your story</p><div class="question"><h1>${question ? question.text : notice ? "A little hiccup." : "I’m thinking of a good question…"}</h1>${audioURL ? html`<button class="speaker" onClick=${() => listen(audioURL, true)}>${needsTap ? "Tap to listen" : "Listen again"}</button>` : null}</div>${waiting ? html`<p class="warm">🐇 A little animal is thinking…</p>` : html`<div class="chips">${(question && question.chips || []).map(chip => html`<button onClick=${() => send(chip)}>${chip}</button>`)}</div>`}${notice ? html`<p class="warm" role="status">${notice}</p>` : null}<form class="answer" onSubmit=${event => { event.preventDefault(); send(answer); }}><input value=${answer} onInput=${e => setAnswer(e.currentTarget.value)} onFocus=${e => e.currentTarget.scrollIntoView({ block: "center" })} placeholder="Or write your own idea" autocomplete="off" /><button class="primary" disabled=${waiting}>Send</button></form></section>`;
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
