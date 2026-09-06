// bookPayload reads one book-stream frame's JSON, treating anything it
// cannot parse as absent. These handlers run inside the app's effects, and
// an exception thrown out of one abandons the rest of that effect — a
// malformed frame must cost this app a progress reading, never the screen.
function bookPayload(event) {
  try {
    return JSON.parse(event?.data ?? "null");
  } catch (_) {
    return null;
  }
}

// subscribeBook establishes the C4 book-event subscription. Callers must wait
// for opened before reading the authoritative state: EventSource construction
// only starts a connection, while open follows the server's subscription.
export function subscribeBook(eventsURL, handlers) {
  const source = new EventSource(eventsURL);
  let terminal = false;
  let resolveOpened;
  const opened = new Promise(resolve => { resolveOpened = resolve; });

  function close() {
    terminal = true;
    source.close();
  }

  let openedOnce = false;
  let connectionEpoch = 0;
  source.addEventListener("open", () => {
    connectionEpoch++;
    resolveOpened();
    if (openedOnce) handlers.reconnected?.(connectionEpoch);
    openedOnce = true;
  });
  source.addEventListener("page_approved", event => {
    const page = bookPayload(event);
    if (!terminal && page) handlers.pageApproved(page);
  });
  // stage is optional to a handler set: it is the only event a caller can
  // ignore and still be correct, because it changes the progress reading and
  // nothing else.
  source.addEventListener("stage", event => {
    const stage = bookPayload(event);
    if (!terminal && stage) handlers.stage?.(stage);
  });
  source.addEventListener("narration_unavailable", () => {
    if (!terminal) handlers.narrationUnavailable();
  });
  source.addEventListener("book_ready", event => {
    if (terminal) return;
    terminal = true;
    handlers.bookReady(bookPayload(event) || {});
    source.close();
  });
  source.addEventListener("failed", () => {
    if (terminal) return;
    terminal = true;
    handlers.failed();
    source.close();
  });

  return { source, opened, close, isTerminal: () => terminal, connectionEpoch: () => connectionEpoch };
}

// readBookState performs C4's subscribe-then-snapshot half. A state-read
// failure leaves the open stream alone: only the stream's failed event says
// that the generation itself has failed.
export async function readBookState(subscription, read) {
  await subscription.opened;
  const epoch = subscription.connectionEpoch();
  if (subscription.isTerminal()) return { terminal: true, epoch };
  try {
    return { state: await read(), epoch };
  } catch (error) {
    return { error, epoch };
  }
}
