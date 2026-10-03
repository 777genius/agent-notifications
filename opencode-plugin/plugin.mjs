import { createObserver } from 'universal-agent-plugins-opencode-events/v1';
import { createV2Observer } from 'universal-agent-plugins-opencode-events/v2';
import { prepareOwnedHost } from './owned-host.mjs';
import { selectClockCell } from './clock-cells.mjs';
import { createPlatformClock } from './platform-clock.mjs';
import { createPreparedDelivery } from './ipc.mjs';
import { createDisplayContext } from './display.ts';
import { parseJSON } from './protocol.mjs';
import { createNativeV1 } from './native-v1.mjs';
import { createNativeV2, createRPCCheckpoint } from './native-v2.mjs';

const servers = new WeakMap(), setups = new WeakMap();
const silent = Object.freeze({ event() {} });
async function reportDelivery(delivery) {
  const result = await delivery;
  if (!result) return;
  let outcome = result.status;
  if (outcome === 'ok') {
    try { outcome = parseJSON(result.output, 1024).status; } catch { outcome = 'invalid_receipt'; }
  }
  if (outcome !== 'submitted' && outcome !== 'suppressed')
    console.error('Agent Notifications OpenCode delivery:',
      ['rejected', 'unavailable', 'unknown', 'invalidated', 'deadline', 'exited', 'capacity',
        'stream_error', 'overflow', 'ipc_termination_unproved', 'invalid_receipt'].includes(outcome) ? outcome : 'unavailable');
}
async function server(input, options = {}) {
  if (!input?.client || typeof input.client.session?.get !== 'function' ||
      typeof input.client.session?.messages !== 'function') return silent;
  const existing = servers.get(input.client);
  if (existing) {
    if (existing.directory !== input.directory) { existing.retired = true; existing.stop?.(); return silent; }
    return existing.starting;
  }
  const holder = { directory: input.directory, retired: false };
  const starting = (async () => {
    const owned = await prepareOwnedHost(input.directory, 'v1', undefined, options?.diagnostics === true);
    if (!owned) return silent;
    if (holder.retired) { await owned.dispose(); return silent; }
    const policy = selectClockCell('v1');
    if (!policy) { await owned.dispose(); return silent; }
    let observer, view, stopped = false, stopping;
    const publications = new WeakSet();
    const display = createDisplayContext(input.client);
    const delivery = createPreparedDelivery({ registry: owned.registry, origin: owned.origin, policy,
      sourceFactory: createPlatformClock, isOwned: owned.isOwned, enrich: display.enrich, onInvalidate: () => observer?.dispose() });
    view = createNativeV1(input.client, input.directory, delivery.invalidate);
    const stop = () => {
      if (stopped) return stopping;
      stopped = true; observer?.dispose(); view.dispose(); delivery.dispose();
      stopping = owned.dispose();
      return stopping;
    };
    holder.stop = stop;
    try {
      if (!await delivery.activate() || holder.retired) { stop(); return silent; }
      observer = createObserver({ client: input.client, location: input.directory,
        runtimeEligibility: owned.runtimeEligibility, callbackAuthority: 'qualified_native_sync', clock: delivery.clock,
        onDiagnostic: reason => console.error('Agent Notifications OpenCode observer:', reason),
        beforeEmit: async (event, handoff) => await delivery.beforeEmit(event, handoff) && await view.finalize(event, handoff),
        emit: (event, handoff) => reportDelivery(delivery.emit(event, handoff)) });
      return Object.freeze({ dispose: stop, event({ event }) {
        // Capture and control reduction occur on the native publication stack.
        // Never await a lookup/child before close/tombstone ingress.
        if (stopped) return;
        if (event && typeof event === 'object') {
          if (publications.has(event)) return; publications.add(event);
        }
        let snapshot;
        try {
          delivery.beginIngress(event);
          if (event?.type === 'location.shutdown' || event?.type === 'server.instance.disposed') { stop(); return; }
          if (event?.type === 'message.updated' && event.properties?.info?.role === 'user' &&
              !delivery.allowsBirth(event.properties.info.time?.created)) return;
          snapshot = display.capture(event);
          view.ingest(event);
          void observer.observe(event).catch(stop).finally(() => display.release(snapshot));
        } catch { display.release(snapshot); stop(); }
      } });
    } catch { stop(); return silent; }
  })();
  holder.starting = starting; servers.set(input.client, holder);
  return starting;
}
async function setup(context) {
  if (!context || typeof context.event?.subscribe !== 'function' || typeof context.session?.get !== 'function' ||
      typeof context.session?.context !== 'function' || typeof context.permission?.get !== 'function' ||
      typeof context.permission?.list !== 'function' || typeof context.rpc?.register !== 'function') return;
  if (setups.has(context)) return setups.get(context);
  const starting = (async () => {
    const owned = await prepareOwnedHost(context.location?.directory, 'v2', context.app?.version, context.options?.diagnostics === true);
    if (!owned) return;
    const policy = selectClockCell('v2');
    // The official V2 session API is not the V1 title lookup API. No synthetic
    // client/events: when that native client is unavailable, display stays neutral.
    const display = context.client?.session?.get ? createDisplayContext(context.client) : undefined;
    if (!policy) { await owned.dispose(); return; }
    let observer, view, stopped = false, connection;
    const location = Object.freeze({ directory: context.location.directory, workspaceID: context.location.workspaceID,
      projectID: context.location.project?.id });
    const isNativeOwned = () => owned.isOwned() && context.app?.version === '2.0.21' &&
      context.location?.directory === location.directory && context.location?.workspaceID === location.workspaceID &&
      context.location?.project?.id === location.projectID;
    const delivery = createPreparedDelivery({ registry: owned.registry, origin: owned.origin, policy,
      sourceFactory: createPlatformClock, isOwned: isNativeOwned, enrich: display?.enrich, onInvalidate: (reason) => { if (reason === 'clock') observer?.dispose(); view?.reset(); connection?.abort(); } });
    const stop = async () => {
      if (stopped) return; stopped = true;
      observer?.dispose(); delivery.dispose(); connection?.abort(); view?.reset();
      return owned.dispose();
    };
    try {
      view = createNativeV2(context, context.location, delivery.nativeIngress, delivery.invalidate);
      if (!await delivery.activate()) { await stop(); return; }
      const readerContext = { app: context.app, event: { subscribe({ signal }) {
        connection = new AbortController();
        const abort = () => { delivery.invalidate('reader'); current.abort(); };
        signal.addEventListener('abort', abort, { once: true });
        const current = connection;
        // SDK owns the only reader/replacement budget. Reconnect gets a closed
        // anchor before opening native subscription/RPC; old identities are gone.
        let iterator, snapshot;
        const opening = delivery.activate().then((ready) => {
          if (!ready || stopped || signal.aborted || current.signal.aborted) throw new TypeError('reader_unverified');
          iterator = context.event.subscribe({ signal: current.signal })[Symbol.asyncIterator]();
        });
        return { [Symbol.asyncIterator]() { return this; }, async next() {
          display?.release(snapshot); snapshot = undefined;
          try {
            await opening;
            const item = await iterator.next();
            if (!item.done) snapshot = display?.capture(item.value);
            if (item.done) delivery.invalidate('reader');
            return item;
          } catch { delivery.invalidate('reader'); throw new TypeError('reader_unverified'); }
        }, async return() {
          display?.release(snapshot); snapshot = undefined;
          signal.removeEventListener('abort', abort);
          delivery.invalidate('reader'); current.abort();
          await opening.catch(() => {});
          return iterator?.return ? iterator.return() : { done: true };
        } };
      } } };
      observer = createV2Observer({ context: readerContext, location: context.location.directory,
        runtimeEligibility: () => isNativeOwned() ? owned.runtimeEligibility() : 'unverified', native: view.native,
        checkpoint: createRPCCheckpoint(context, delivery.activate), clock: delivery.clock,
        onDiagnostic: reason => console.error('Agent Notifications OpenCode observer:', reason),
        beforeEmit: async (event, handoff) => { const fact = view.project(event); return Boolean(fact &&
          await delivery.beforeEmit(fact, handoff) && await view.finalize(event, handoff)); },
        emit: (event, handoff) => { const fact = view.project(event); return fact ? reportDelivery(delivery.emit(fact, handoff)) : undefined; } });
      observer.start();
      return stop;
    } catch { await stop(); }
  })();
  setups.set(context, starting);
  return starting;
}

// Retained named legacy entry and modern object route share ONE startup per
// native client. Discovery of both exports cannot create two authority grants.
export const AgentNotifications = (input, options) => server(input, options);
export default Object.freeze({ id: 'agent-notifications', server, setup });
