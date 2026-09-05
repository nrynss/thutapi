import { h, render } from "/static/vendor/preact.module.js";
import htm from "/static/vendor/htm.module.js";
import { useEffect, useRef, useState } from "/static/vendor/hooks.module.js";

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

function Shelf({ onStart }) {
  return html`<section class="card shelf-card"><p class="eyebrow">Thutapi picture books</p><h1>Stories made from your ideas.</h1><p>Pick a cozy book from the shelf, or make one with a grown-up.</p><a class="primary" href="/interview/new" data-start onClick=${onStart}>Make your own book</a><p class="warm">One tap starts our story.</p></section>`;
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
      bookStream.current && bookStream.current.close();
      const source = new EventSource(start.events_url);
      bookStream.current = source;
      source.addEventListener("page_approved", event => {
        const page = JSON.parse(event.data).n;
        if (!Number.isInteger(page) || page < 1 || page > 8 || approvedPages.current.has(page)) return;
        approvedPages.current.add(page);
        setDone(approvedPages.current.size);
      });
      source.addEventListener("narration_unavailable", () => setBookState("quiet"));
      source.addEventListener("book_ready", event => { const ready = JSON.parse(event.data); source.close(); window.location.assign(`/book/${start.book_id || ready.book_id || ""}`); });
      source.addEventListener("failed", () => { source.close(); setBookState("failed"); });
    } catch (_) {
      setBookState("failed");
    }
  }

  useEffect(() => {
    // T13 owns both capture controls. Until it mounts its complete interface,
    // this is the meaningful no-sample path: end the interview and draw.
    if (!ended || generationStarted.current) return;
    generationStarted.current = true;
    generate();
  }, [ended]);

  if (bookState) return html`<section class="card wait"><p class="eyebrow">Your book is on its way</p><h1>${bookState === "failed" ? "The animals need a little rest." : "The animals are drawing your story."}</h1><${Race} done=${done} sitting=${bookState === "failed"} />${bookState === "quiet" ? html`<p class="warm">Your book will be beautifully captioned and quiet today.</p>` : null}${bookState === "failed" ? html`<div class="doors"><button class="primary" onClick=${generate}>Try again</button><a class="secondary" href="/">Look at other books</a></div>` : null}</section>`;
  if (missing) return html`<section class="card"><p class="eyebrow">A tiny detour</p><h1>That story wandered away.</h1><p class="warm" role="status">Start a new story and we’ll make a fresh little path together.</p><div class="doors"><a class="primary" href="/">Start a new story</a><a class="secondary" href="/">Look at other books</a></div></section>`;
  return html`<section class="interview"><p class="eyebrow">Your story</p><div class="question"><h1>${question ? question.text : "I’m thinking of a good question…"}</h1>${audioURL ? html`<button class="speaker" onClick=${() => listen(audioURL, true)}>${needsTap ? "Tap to listen" : "Listen again"}</button>` : null}</div>${waiting ? html`<p class="warm">🐇 A little animal is thinking…</p>` : html`<div class="chips">${(question && question.chips || []).map(chip => html`<button onClick=${() => send(chip)}>${chip}</button>`)}</div>`}${notice ? html`<p class="warm" role="status">${notice}</p>` : null}<form class="answer" onSubmit=${event => { event.preventDefault(); send(answer); }}><input value=${answer} onInput=${e => setAnswer(e.currentTarget.value)} onFocus=${e => e.currentTarget.scrollIntoView({ block: "center" })} placeholder="Or write your own idea" autocomplete="off" /><button class="primary" disabled=${waiting}>Send</button></form></section>`;
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
if (root) render(html`<${App} initialRoute=${root.dataset.interviewId || "shelf"} />`, root);
