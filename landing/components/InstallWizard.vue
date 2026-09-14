<script setup lang="ts">
import {
  command,
  detectTarget,
  products,
  targets,
  repo,
  type Product,
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
const product = ref<Product>("claude");
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
  product.value === "both"
    ? t("install.products.both")
    : product.value === "claude"
      ? "Claude Code"
      : "Codex CLI",
);
const osLabel = computed(
  () =>
    localizedTargets.value.find((item) => item.value === target.value)?.label ??
    t("install.targets.unknown"),
);
const copyStatus = ref("");
const commandField = ref<HTMLTextAreaElement>();
const agentNotify = ref(true);
const snippet = computed(() =>
  command(product.value, target.value, intent.value, agentNotify.value),
);
const displaySnippet = computed(() => snippet.value?.replace(" | ", " |\n"));
onMounted(() => {
  detected.value = detectTarget(navigator.userAgent, navigator.maxTouchPoints);
  if (!manualOverride.value) target.value = detected.value;
  showOSPicker.value = target.value === "unknown";
});
watch([product, target, intent, agentNotify], () => {
  copyStatus.value = "";
});
async function copy() {
  const value = snippet.value;
  if (!value) return;
  try {
    await navigator.clipboard.writeText(value);
    if (snippet.value === value)
      copyStatus.value = t("install.copied");
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

    <div class="agent-cards" role="group" :aria-label="t('install.chooseAgent')">
      <button
        v-for="item in localizedProducts.filter((item) => item.value !== 'both')"
        :key="item.value"
        class="agent-card"
        :aria-label="item.label"
        :aria-pressed="product === item.value"
        @click="product = item.value"
      >
        <AgentLogo :agent="item.value as 'claude' | 'codex'" />
        <span class="agent-card-copy"
          ><strong
            >{{ item.value === "claude" ? "Claude Code" : "Codex CLI" }}
            <small v-if="item.value === 'codex'">{{ t("common.beta") }}</small></strong
          ><span>{{ t("install.requires", { agent: item.value === "claude" ? "Claude Code" : "Codex CLI" }) }}</span
          ></span
        >
        <span class="agent-check" aria-hidden="true">{{
          product === item.value ? "✓" : ""
        }}</span>
      </button>
    </div>
    <div class="environment-row">
      <div class="os-summary">
        <span>{{
          target === "unknown"
            ? t("install.chooseComputer")
            : osLabel
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
      <button
        class="text-action both-choice"
        :aria-pressed="product === 'both'"
        :aria-label="t('install.bothAgents')"
        @click="product = product === 'both' ? 'claude' : 'both'"
      >
        {{
          product === "both"
            ? t("install.bothSelected")
            : t("install.installBoth")
        }}
      </button>
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

    <p v-if="product !== 'claude'" class="notice install-prerequisite">
      <strong>{{ t("install.prerequisiteTitle") }}</strong>
      {{ t("install.prerequisiteText") }}
      <a :href="repo + '/releases'">{{ t("install.checkReleases") }}</a>.
    </p>

    <label
      v-if="intent !== 'configure' && target !== 'manual' && target !== 'unknown'"
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
      <h3>{{ t("install.configure.title") }}</h3>
      <p v-if="product !== 'codex'">
        {{ t("install.configure.claudeBefore") }}
        <code>/claude-notifications-go:settings</code>.
        {{ t("install.configure.claudeAfter") }}
      </p>
      <p v-if="product !== 'claude'">
        {{ t("install.configure.codexBefore") }}
        <code>config path</code>
        {{ t("install.configure.codexMiddle") }}
        <a :href="repo + '#manual-configuration'"
          >{{ t("install.configure.codexLink") }}</a
        >. {{ t("install.configure.codexAfter") }}
      </p>
      <p>{{ t("install.configure.shared") }}</p>
    </div>
    <div v-else-if="target === 'manual'" class="setup-panel instructions">
      <h3>{{ t("install.manual.title") }}</h3>
      <p v-if="product !== 'codex'">
        <a :href="repo + '#manual-install'">{{ t("install.manual.claude") }}</a>
      </p>
      <p v-if="product !== 'claude'">
        <a :href="repo + '#manual-codex-registration'"
          >{{ t("install.manual.codex") }}</a
        >
      </p>
    </div>
    <div v-else-if="!snippet" class="setup-panel instructions">
      <p>{{ t("install.chooseTarget") }}</p>
    </div>
    <template v-else>
      <div class="installation-command">
        <div class="command-panel-heading">
          <label for="command">{{ t("install.commandLabel", { intent: t(`install.intents.${intent === "update" ? "update" : "install"}`) }) }}</label>
          <div class="command-tools">
            <span>{{ target === "windows" ? "Git Bash" : "Bash" }}</span>

          </div>
        </div>
        <div class="install-command-line">
            <button class="copy-icon" :aria-label="t('install.copyCommand')" :title="t('install.copyCommand')" @click="copy">
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
                <path d="M16 18v3H4V7h4" /></svg
              >
            </button>
          <span class="terminal-prompt" aria-hidden="true">$</span>
          <textarea
            id="command"
            ref="commandField"
            :value="displaySnippet"
            readonly
            spellcheck="false"
            rows="2"
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
              {{ target === "windows" ? t("install.steps.runBash") : t("install.steps.runTerminal") }}
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
            <h3>{{ t("install.steps.restart", { agent: product === "both" ? t("install.steps.bothAgents") : agentName }) }}</h3>
            <p v-if="product !== 'codex'">
              {{ t("install.steps.restartClaude") }}
            </p>
            <p v-if="product !== 'claude'">
              {{ t("install.steps.restartCodex") }}
            </p>
          </div>
        </article>
      </div>
    </template>
    <footer class="install-footer">
      <a
        :href="
          repo +
          (product === 'claude'
            ? '#manual-install'
            : '#manual-codex-registration')
        "
        >{{ t("install.help") }} ↗</a
      >
      <a v-if="product === 'both'" :href="repo + '#manual-install'"
        >{{ t("install.claudeHelp") }} ↗</a
      >
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
