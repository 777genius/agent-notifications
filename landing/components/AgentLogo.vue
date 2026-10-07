<script setup lang="ts">
defineProps<{ agent: "claude" | "codex" | "opencode" | "gemini" }>();
const base = useRuntimeConfig().app.baseURL;
</script>
<template>
  <img
    class="agent-logo"
    :src="`${base}agents/${agent}.${agent === 'claude' ? 'png' : 'svg'}`"
    :alt="agent === 'claude' ? 'Claude' : agent === 'codex' ? 'Codex CLI' : agent === 'opencode' ? 'OpenCode' : 'Gemini CLI'"
    :title="agent === 'gemini' ? 'Gemini CLI (Linux / Windows)' : undefined"
    width="28"
    height="28"
  />
</template>
