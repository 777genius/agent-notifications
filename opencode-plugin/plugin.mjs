import { createObserver } from 'universal-agent-plugins-opencode-events';
import { forward } from './ipc.mjs';

export const AgentNotifications = async ({ client }) => {
  const observer = createObserver({
    client,
    emit: async (event) => {
      if (event.kind === 'unknown' || event.rootSession !== true) return;
      const outcome = await forward(event);
      if (outcome !== 'submitted' && outcome !== 'suppressed') {
        console.error('Agent Notifications OpenCode delivery:', outcome);
      }
    },
    onDiagnostic: (reason) => console.error('Agent Notifications OpenCode observer:', reason),
  });
  return { event: async ({ event }) => observer.observe(event) };
};
