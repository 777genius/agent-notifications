<script setup lang="ts">
defineProps<{ agent: "claude" | "codex" | "opencode" }>();
const base = useRuntimeConfig().app.baseURL;
</script>
<template>
  <img
    class="agent-logo"
    :src="`${base}agents/${agent}.${agent === 'claude' ? 'png' : 'svg'}`"
    :alt="agent === 'claude' ? 'Claude Code' : agent === 'codex' ? 'Codex CLI' : 'OpenCode'"
    width="28"
    height="28"
  />
</template>
