import { createObserver } from 'universal-agent-plugins-opencode-events';
import { forward } from './ipc.mjs';
import { createDisplayContext } from './display.ts';

export const AgentNotifications = async ({ client }) => {
  const display = createDisplayContext(client);
  const observer = createObserver({
    client,
    emit: async (event) => {
      if (event.kind === 'unknown' || event.rootSession !== true) return;
      const outcome = await forward(await display.enrich(event));
      if (outcome !== 'submitted' && outcome !== 'suppressed') {
        console.error('Agent Notifications OpenCode delivery:', outcome);
      }
    },
    onDiagnostic: (reason) => console.error('Agent Notifications OpenCode observer:', reason),
  });
  return { event: async ({ event }) => {
    const snapshot = display.capture(event);
    try { await observer.observe(event); } finally { display.release(snapshot); }
  } };
};
