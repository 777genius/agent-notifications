import { createObserver, createV2Observer } from 'universal-agent-plugins-opencode-events';
import { forward, createBoundedForwarder } from './ipc.mjs';

const diagnostic = (reason) => console.error('Agent Notifications OpenCode observer:', reason);
const emitter = (deliver) => async (event) => {
  if (event.kind === 'unknown' || event.rootSession !== true) return;
  const outcome = await deliver(event);
  if (outcome !== 'submitted' && outcome !== 'suppressed') {
    console.error('Agent Notifications OpenCode delivery:', outcome);
  }
};

async function server({ client }) {
  const observer = createObserver({
    client,
    emit: emitter(forward),
    onDiagnostic: diagnostic,
  });
  return { event: async ({ event }) => observer.observe(event) };
}

function setup(ctx) {
  if (typeof ctx?.session?.get !== 'function' || typeof ctx?.session?.context !== 'function' || typeof ctx?.event?.subscribe !== 'function') {
    diagnostic('invalid_host');
    return;
  }
  const controller = new AbortController();
  const delivery = createBoundedForwarder();
  let observer;
  try {
    observer = createV2Observer({ client: ctx.session, location: ctx.location, emit: emitter(delivery.forward), onDiagnostic: diagnostic });
  } catch {
    delivery.dispose();
    diagnostic('invalid_host');
    return;
  }
  let disposed = false;
  let iterator;
  const stop = () => {
    if (disposed) return;
    disposed = true;
    observer.dispose();
    delivery.dispose();
    controller.abort();
    try { void Promise.resolve(iterator?.return?.()).catch(() => {}); } catch {}
  };
  void (async () => {
    try {
      iterator = ctx.event.subscribe({ signal: controller.signal })[Symbol.asyncIterator]();
      for (;;) {
        const next = await iterator.next();
        if (disposed || next.done) break;
        // observe reduces metadata synchronously before its asynchronous checks.
        // Replies/cancellations must never wait behind a lookup or child process.
        void Promise.resolve(observer.observe(next.value)).catch(() => { if (!disposed) diagnostic('observation_failed'); });
      }
    } catch {
      if (!disposed) diagnostic('subscription_failed');
    } finally {
      stop();
    }
  })();
  return stop;
}

export default { id: 'agent-notifications', server, setup };
