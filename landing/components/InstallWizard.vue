<script setup lang="ts">
import {
  command,
  detectTarget,
  products,
  targets,
  repo,
  type AgentProduct,
  type Target,
  type Intent,
} from "~/data/install";
const { t } = useI18n();
const installTitle = ref<HTMLHeadingElement>();
async function changeIntent(value: Intent) {
  intent.value = value;
  await nextTick();
  installTitle.value?.focus({ preventScroll: true });
}
const selectedProducts = reactive({ claude: true, codex: false, opencode: false, gemini: false });
const selection = computed<AgentProduct[]>(() =>
  (["claude", "codex", "opencode", "gemini"] as const).filter((value) => selectedProducts[value]),
);
const hasObserver = computed(() => selectedProducts.opencode || selectedProducts.gemini);
const hasLegacy = computed(() => selectedProducts.claude || selectedProducts.codex);
function toggleProduct(value: AgentProduct) {
  if (selectedProducts[value] && selection.value.length === 1) return;
  selectedProducts[value] = !selectedProducts[value];
}
const target = ref<Target>("unknown");
const intent = ref<Intent>("install");
const manualOverride = ref(false);
const detected = ref<Target>("unknown");
const showOSPicker = ref(false);
const localizedProducts = computed(() =>
  products.map((item) => ({
    ...item,
    label: t(`install.products.${item.value}`),
  })),
);
const localizedTargets = computed(() =>
  targets.map((item) => ({
    ...item,
    label: t(`install.targets.${item.value}`),
  })),
);
const agentName = computed(() =>
  selection.value.map((value) => t(`install.products.${value}`)).join(" + "),
);
const osLabel = computed(
  () =>
    localizedTargets.value.find((item) => item.value === target.value)?.label ??
    t("install.targets.unknown"),
);
const copyStatus = ref("");
const commandField = ref<HTMLTextAreaElement>();
const agentNotify = ref(true);
const observerChannels = reactive({ desktop: true, webhook: false });
const snippet = computed(() =>
  command(selection.value, target.value, intent.value, agentNotify.value, observerChannels),
);
const displaySnippet = computed(() => snippet.value);
onMounted(() => {
  detected.value = detectTarget(navigator.userAgent, navigator.maxTouchPoints);
  if (!manualOverride.value) target.value = detected.value;
  showOSPicker.value = target.value === "unknown";
});
watch([selection, target, intent, agentNotify, () => observerChannels.desktop, () => observerChannels.webhook], () => {
  copyStatus.value = "";
});
async function copy() {
  const value = snippet.value;
  if (!value) return;
  try {
    await navigator.clipboard.writeText(value);
    if (snippet.value === value) copyStatus.value = t("install.copied");
  } catch {
    if (snippet.value !== value) return;
    copyStatus.value = t("install.copyUnavailable");
    commandField.value?.focus();
    commandField.value?.select();
  }
}
</script>
<template>
  <section
    id="install"
    class="section anchor-offset guided-install"
    aria-labelledby="install-title"
  >
    <header class="install-heading">
      <h2 id="install-title" ref="installTitle" tabindex="-1">
        {{ t("install.title") }}
      </h2>
      <p>{{ t("install.intro", { agent: agentName }) }}</p>
    </header>

    <div
      class="agent-cards"
      role="group"
      :aria-label="t('install.chooseAgent')"
    >
      <button
        v-for="item in localizedProducts.filter(
          (item) => item.value !== 'both',
        )"
        :key="item.value"
        class="agent-card"
        :aria-label="item.label"
        :aria-pressed="selectedProducts[item.value as AgentProduct]"
        @click="toggleProduct(item.value as AgentProduct)"
      >
        <AgentLogo :agent="item.value as AgentProduct" />
        <span class="agent-card-copy"
          ><strong>{{
            item.label
          }}</strong
          ><span>{{
            t("install.requires", {
              agent: item.label,
            })
          }}</span></span
        >
        <span class="agent-check" aria-hidden="true">{{
          selectedProducts[item.value as AgentProduct] ? "✓" : ""
        }}</span>
      </button>
    </div>
    <div class="environment-row">
      <div class="os-summary">
        <span>{{
          target === "unknown" ? t("install.chooseComputer") : osLabel
        }}</span>
        <small v-if="target !== 'unknown' && target !== 'manual'">{{
          manualOverride ? t("install.selected") : t("install.detected")
        }}</small>
        <button
          class="text-action"
          :aria-expanded="showOSPicker"
          aria-controls="os-picker"
          @click="showOSPicker = !showOSPicker"
        >
          {{ t("install.change") }}
        </button>
      </div>
    </div>
    <div v-if="showOSPicker" id="os-picker" class="os-picker">
      <AppSelect
        :model-value="target"
        :options="localizedTargets"
        :label="t('install.targetOs')"
        @update:model-value="
          target = $event as Target;
          manualOverride = true;
        "
      />
    </div>

    <div class="selected-agent-info" role="group" :aria-label="t('install.capabilities.title')">
      <article v-for="agent in selection" :key="agent" class="selected-agent-details">
        <h3>{{ t(`install.products.${agent}`) }}</h3>
        <p>{{ t(`install.capabilities.${agent}`) }}</p>
        <details v-if="agent !== 'claude'">
          <summary>{{ t('install.capabilities.details') }}</summary>
          <p>{{ agent === 'codex' ? t('install.prerequisiteText') : t(`install.${agent}.version`) }}</p>
          <a :href="repo + (agent === 'codex' ? '/releases' : `/blob/main/docs/${agent}-notifications.md`)">
            {{ agent === 'codex' ? t('install.checkReleases') : t(`install.${agent}.guide`) }}
          </a>
        </details>
      </article>
    </div>
    <fieldset
      v-if="hasObserver && intent !== 'configure' && target !== 'manual'"
      class="observer-channels"
      aria-describedby="observer-channels-hint"
    >
      <legend>{{ t('install.observer.channelsTitle') }}</legend>
      <p id="observer-channels-hint">{{ t('install.observer.channelsHint') }}</p>
      <div class="channel-options">
        <label class="agent-notify-option">
          <input v-model="observerChannels.desktop" type="checkbox" />
          <span>{{ t('install.opencode.desktop') }}</span>
        </label>
        <label class="agent-notify-option">
          <input v-model="observerChannels.webhook" type="checkbox" />
          <span>{{ t('install.opencode.webhook') }}</span>
        </label>
      </div>
    </fieldset>
    <label
      v-if="hasLegacy && intent !== 'configure' && target !== 'manual' && target !== 'unknown'"
      class="agent-notify-option"
    >
      <input
        v-model="agentNotify"
        type="checkbox"
        :aria-label="t('install.agentNotify.label')"
      />
      <span>
        <strong>{{ t("install.agentNotify.label") }}</strong>
        <small>{{ t("install.agentNotify.hint") }}</small>
      </span>
    </label>

    <div
      v-if="intent === 'configure'"
      class="setup-panel configuration instructions"
    >
      <h3>{{ hasLegacy ? t("install.configure.title") : t("install.capabilities.title") }}</h3>
      <p v-if="selectedProducts.claude">
        {{ t("install.configure.claudeBefore") }}
        <code>/claude-notifications-go:settings</code>.
        {{ t("install.configure.claudeAfter") }}
      </p>
      <p v-if="selectedProducts.codex">
        {{ t("install.configure.codexBefore") }}
        <code>config path</code>
        {{ t("install.configure.codexMiddle") }}
        <a
          :href="repo + '/blob/main/docs/CONFIGURATION.md#manual-configuration'"
          >{{ t("install.configure.codexLink") }}</a
        >. {{ t("install.configure.codexAfter") }}
      </p>
      <p v-if="selectedProducts.opencode">{{ t("install.opencode.configure") }}</p>
      <p v-if="selectedProducts.gemini"><a :href="repo + '/blob/main/docs/gemini-notifications.md'">{{ t("install.gemini.guide") }}</a>. {{ t("install.gemini.configure") }}</p>
      <p v-if="hasLegacy">{{ t("install.configure.shared") }}</p>
    </div>
    <div v-else-if="selectedProducts.gemini && target !== 'manual'" class="setup-panel instructions" role="status">
      <p>{{ t("install.gemini.version") }}</p>
      <p v-if="!observerChannels.desktop && !observerChannels.webhook">{{ t("install.opencode.chooseChannel") }}</p>
      <a :href="repo + '/blob/main/docs/gemini-notifications.md'">{{ t("install.gemini.guide") }}</a>
    </div>
    <div v-else-if="target === 'manual'" class="setup-panel instructions">
      <h3>{{ t("install.manual.title") }}</h3>
      <p v-if="selectedProducts.claude">
        <a :href="repo + '/blob/main/docs/INSTALLATION.md#manual-install'">{{
          t("install.manual.claude")
        }}</a>
      </p>
      <p v-if="selectedProducts.codex">
        <a
          :href="repo + '/blob/main/docs/CODEX.md#manual-codex-registration'"
          >{{ t("install.manual.codex") }}</a
        >
      </p>
      <p v-if="selectedProducts.gemini">{{ t("install.gemini.version") }} <a :href="repo + '/blob/main/docs/gemini-notifications.md'">{{ t("install.gemini.guide") }}</a></p>
    </div>
    <div v-else-if="!snippet && hasObserver && !observerChannels.desktop && !observerChannels.webhook" class="setup-panel instructions"><p>{{ t("install.opencode.chooseChannel") }}</p></div>
    <div v-else-if="!snippet" class="setup-panel instructions">
      <p>{{ t("install.chooseTarget") }}</p>
    </div>
    <template v-else>
      <div class="installation-command">
        <div class="command-panel-heading">
          <label for="command">{{
            t("install.commandLabel", {
              intent: t(
                `install.intents.${intent === "update" ? "update" : "install"}`,
              ),
            })
          }}</label>
          <div class="command-tools">
            <span>{{ target === "windows" ? "Git Bash" : "Bash" }}</span>
          </div>
        </div>
        <div class="install-command-line" dir="ltr">
          <button
            class="copy-icon"
            :aria-label="t('install.copyCommand')"
            :title="t('install.copyCommand')"
            @click="copy"
          >
            <svg
              width="18"
              height="18"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.6"
              aria-hidden="true"
            >
              <rect x="8" y="3" width="12" height="15" rx="2" />
              <path d="M16 18v3H4V7h4" />
            </svg>
          </button>
          <span class="terminal-prompt" aria-hidden="true">$</span>
          <textarea
            id="command"
            ref="commandField"
            :value="displaySnippet"
            readonly
            spellcheck="false"
            :rows="displaySnippet?.split('\n').length ?? 1"
            wrap="off"
          />
        </div>
        <button
          v-if="intent === 'install'"
          class="text-action update-under-command"
          :aria-label="t('install.intents.update')"
          @click="changeIntent('update')"
        >
          {{ t("install.updatingInstead") }}
        </button>
        <p role="status" class="copy-status">{{ copyStatus }}</p>
        <p v-if="intent === 'update'" class="update-note">
          {{ t("install.updateNote") }}
        </p>
      </div>
      <div class="next-steps">
        <article class="setup-panel next-step">
          <span class="step-icon" aria-hidden="true"
            ><svg
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.7"
            >
              <path d="m5 6 6 6-6 6M13 18h6" /></svg
          ></span>
          <div>
            <span class="step-label">{{ t("install.steps.label1") }}</span>
            <h3>
              {{
                target === "windows"
                  ? t("install.steps.runBash")
                  : t("install.steps.runTerminal")
              }}
            </h3>
            <p v-if="target === 'windows'">
              <strong>{{ t("install.steps.windowsTitle") }}</strong>
              {{ t("install.steps.windowsText") }}
            </p>
            <p v-else>{{ t("install.steps.terminalText") }}</p>
          </div>
        </article>
        <article class="setup-panel next-step">
          <span class="step-icon" aria-hidden="true"
            ><svg
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.7"
            >
              <path d="M20 10a8 8 0 1 0-1 7M20 4v6h-6" /></svg
          ></span>
          <div>
            <span class="step-label">{{ t("install.steps.label2") }}</span>
            <h3>
              {{
                t("install.steps.restart", {
                  agent: agentName,
                })
              }}
            </h3>
            <p v-if="selectedProducts.claude">
              {{ t("install.steps.restartClaude") }}
            </p>
            <p v-if="selectedProducts.codex">
              {{ t("install.steps.restartCodex") }}
            </p>
            <p v-if="selectedProducts.opencode">{{ t("install.opencode.restart") }}</p>
          </div>
        </article>
      </div>
    </template>
    <footer class="install-footer">
      <a v-if="selectedProducts.gemini" :href="repo + '/blob/main/docs/gemini-notifications.md'">{{ t("install.gemini.guide") }} ↗</a>
      <a
        v-if="hasLegacy"
        :href="repo + (selectedProducts.codex
          ? '/blob/main/docs/CODEX.md#manual-codex-registration'
          : '/blob/main/docs/INSTALLATION.md#manual-install')"
      >{{ t("install.help") }} ↗</a>
      <a
        v-if="selectedProducts.claude && selectedProducts.codex"
        :href="repo + '/blob/main/docs/INSTALLATION.md#manual-install'"
      >{{ t("install.claudeHelp") }} ↗</a>
      <a
        v-if="selectedProducts.opencode"
        :href="repo + '/blob/main/docs/opencode-notifications.md'"
      >{{ t("install.opencode.guide") }} ↗</a>
      <div>
        <button
          v-if="intent !== 'install'"
          class="text-action"
          :aria-label="t('install.intents.install')"
          @click="changeIntent('install')"
        >
          {{ t("install.installationInstructions") }}
        </button>

        <button
          v-if="intent !== 'configure'"
          class="text-action"
          :aria-label="t('install.intents.configure')"
          @click="changeIntent('configure')"
        >
          {{ t("install.configureSettings") }}
        </button>
      </div>
    </footer>
  </section>
</template>

<style scoped>
.selected-agent-info, .observer-channels {
  margin: 0 40px 22px;
  padding: 18px;
  border: 1px solid #293648;
  border-radius: 10px;
}
.selected-agent-info { display: grid; gap: 16px; }
.selected-agent-details h3 { margin: 0 0 6px; font-size: 15px; }
.selected-agent-details p, .observer-channels p {
  margin: 0 0 8px;
  color: #9eafc9;
  font-size: 13px;
  line-height: 1.6;
}
.selected-agent-details summary { cursor: pointer; font-size: 13px; }
.selected-agent-details details p { margin-top: 8px; }
.selected-agent-details a { font-size: 13px; text-decoration: underline; }
.observer-channels legend { padding: 0 6px; font-weight: 650; font-size: 15px; }
.channel-options { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.channel-options .agent-notify-option { margin: 0; padding: 12px; align-items: center; }
.channel-options .agent-notify-option input { margin-top: 0; }
.channel-options .agent-notify-option span { font-size: 14px; line-height: 1.5; }
@media (max-width: 700px) {
  .selected-agent-info, .observer-channels { margin-inline: 0; }
  .channel-options { grid-template-columns: 1fr; }
}
</style>
