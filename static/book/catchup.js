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
    if (!terminal) handlers.pageApproved(JSON.parse(event.data));
  });
  source.addEventListener("narration_unavailable", () => {
    if (!terminal) handlers.narrationUnavailable();
  });
  source.addEventListener("book_ready", event => {
    if (terminal) return;
    terminal = true;
    handlers.bookReady(JSON.parse(event.data));
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
