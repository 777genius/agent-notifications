<script setup lang="ts">
import { repo, type AgentProduct } from "~/data/install";

const { t } = useI18n();
const agents: AgentProduct[] = ["claude", "codex", "opencode"];
type Support = "yes" | "no" | "conditional";
const rows: { key: string; support: Record<AgentProduct, Support> }[] = [
  { key: "completion", support: { claude: "yes", codex: "yes", opencode: "yes" } },
  { key: "question", support: { claude: "yes", codex: "conditional", opencode: "yes" } },
  { key: "permission", support: { claude: "yes", codex: "yes", opencode: "yes" } },
  { key: "review", support: { claude: "yes", codex: "no", opencode: "no" } },
  { key: "plan", support: { claude: "yes", codex: "no", opencode: "no" } },
  { key: "limit", support: { claude: "yes", codex: "conditional", opencode: "no" } },
  { key: "error", support: { claude: "yes", codex: "conditional", opencode: "yes" } },
  { key: "sound", support: { claude: "yes", codex: "yes", opencode: "no" } },
  { key: "focus", support: { claude: "conditional", codex: "conditional", opencode: "no" } },
  { key: "webhook", support: { claude: "yes", codex: "yes", opencode: "yes" } },
];
const icons = { yes: "✓", no: "✕", conditional: "✓*" };
</script>

<template>
  <div class="agent-support">
    <div class="agent-support-scroll" role="region" :aria-label="t('agentSupport.title')" tabindex="0">
      <table class="agent-support-table" aria-describedby="agent-support-notes">
        <caption>{{ t("agentSupport.title") }}</caption>
        <thead>
          <tr>
            <th scope="col">{{ t("agentSupport.feature") }}</th>
            <th v-for="agent in agents" :key="agent" scope="col">
              <span class="agent-support-heading">
                <AgentLogo :agent="agent" />
                <span>{{ t(`install.products.${agent}`) }}</span>
              </span>
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.key">
            <th scope="row">{{ t(`agentSupport.rows.${row.key}`) }}</th>
            <td v-for="agent in agents" :key="agent">
              <span :class="['support-mark', `support-${row.support[agent]}`]" :title="t(`agentSupport.${row.support[agent]}`)">
                <span aria-hidden="true">{{ icons[row.support[agent]] }}</span>
                <span class="sr-only">{{ t(`agentSupport.${row.support[agent]}`) }}</span>
              </span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div id="agent-support-notes" class="agent-support-notes">
      <p>{{ t("agentSupport.legend") }}</p>
      <details>
        <summary>{{ t("install.capabilities.details") }}</summary>
        <p>{{ t("agentSupport.conditions") }}</p>
        <p>{{ t("agentSupport.qualification") }}</p>
        <p>{{ t("install.opencode.scope") }}</p>
        <p><strong>Codex CLI:</strong> {{ t("install.prerequisiteText") }} <a :href="repo + '/blob/main/docs/CODEX.md'">{{ t("install.products.codex") }} ↗</a></p>
        <p><strong>OpenCode:</strong> {{ t("install.opencode.version") }} <a href="https://opencode.ai/v2/docs">{{ t("agentSupport.v2Link") }} ↗</a></p>
        <p><a :href="repo + '/blob/main/docs/opencode-notifications.md'">{{ t("install.opencode.guide") }} ↗</a></p>
      </details>
    </div>
  </div>
</template>

<style scoped>
.sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }
.agent-support { margin: 0 40px 22px; border: 1px solid #293648; border-radius: 10px; overflow: hidden; }
.agent-support-scroll { overflow-x: auto; }
.agent-support-scroll:focus-visible { outline: 2px solid #a78bfa; outline-offset: -2px; }
.agent-support-table { width: 100%; border-collapse: collapse; font-size: 13px; }
caption { padding: 16px 18px; text-align: start; font-size: 15px; font-weight: 650; }
th, td { padding: 10px 14px; border-top: 1px solid #293648; }
thead th { color: #c5d0e3; font-weight: 600; background: #ffffff03; }
thead th:first-child, tbody th { text-align: start; }
tbody th { color: #9eafc9; font-weight: 450; }
td { text-align: center; }
.agent-support-heading { display: inline-flex; align-items: center; gap: 8px; white-space: nowrap; }
.agent-support-heading .agent-logo { width: 22px; height: 22px; }
.support-mark { display: inline-block; min-width: 30px; font-size: 17px; font-weight: 650; }
.support-yes { color: #80d8b0; }
.support-no { color: #71809a; }
.support-conditional { color: #d6bd8b; }
.agent-support-notes { padding: 12px 18px 16px; border-top: 1px solid #293648; color: #9eafc9; font-size: 12px; line-height: 1.6; }
.agent-support-notes p { margin: 0 0 8px; }
.agent-support-notes summary { cursor: pointer; color: #c5d0e3; }
.agent-support-notes details p { margin: 8px 0 0; }
.agent-support-notes a { text-decoration: underline; }
@media (max-width: 700px) {
  .agent-support { margin-inline: 0; }
  th, td { padding: 9px 10px; }
  .agent-support-heading { flex-direction: column; gap: 4px; white-space: normal; }
  thead th:not(:first-child) { width: 22%; }
}
</style>
