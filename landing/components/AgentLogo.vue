<script setup lang="ts">
defineProps<{ agent: "claude" | "codex" }>();
const base = useRuntimeConfig().app.baseURL;
</script>
<template>
  <img
    class="agent-logo"
    :src="`${base}agents/${agent}.${agent === 'claude' ? 'png' : 'svg'}`"
    :alt="agent === 'claude' ? 'Claude Code' : 'Codex CLI'"
    width="28"
    height="28"
  />
</template>
