<script setup lang="ts">
defineProps<{ agent: "claude" | "codex" | "opencode" | "gemini" }>();
const base = useRuntimeConfig().app.baseURL;
</script>
<template>
  <span v-if="agent === 'gemini'" class="gemini-textmark" role="img" aria-label="Gemini CLI">Gemini</span>
  <img v-else
    class="agent-logo"
    :src="`${base}agents/${agent}.${agent === 'claude' ? 'png' : 'svg'}`"
    :alt="agent === 'claude' ? 'Claude Code' : agent === 'codex' ? 'Codex CLI' : 'OpenCode'"
    width="28"
    height="28"
  />
</template>

<style scoped>
.gemini-textmark { font-size: 12px; font-weight: 650; flex-shrink: 0; }
</style>
