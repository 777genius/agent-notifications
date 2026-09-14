<script setup lang="ts">
const { t } = useI18n();
const brandLogo = `${useRuntimeConfig().app.baseURL}agent-notifications-logo.png`;
const notificationDefinitions = [
  { key: "question", agent: "claude" },
  { key: "plan", agent: "claude" },
  { key: "completed", agent: "codex", detail: true },
  { key: "review", agent: "claude" },
  { key: "permission", agent: "codex" },
  { key: "limit", agent: "claude" },
  { key: "error", agent: "codex" },
] as const;
const projectNames = [
  "checkout-service",
  "patient-portal",
  "payments-api",
  "mobile-app",
  "analytics-pipeline",
  "customer-dashboard",
  "design-system",
] as const;
const notifications = computed(() =>
  notificationDefinitions.map((item) => ({
    agent: item.agent,
    title: t(`preview.items.${item.key}.title`),
    body: t(`preview.items.${item.key}.body`),
    ...("detail" in item
      ? { detail: t(`preview.items.${item.key}.detail`) }
      : {}),
  })),
);
const cursor = ref(0);
const paused = ref(false);
const reduced = ref(false);
const visible = computed(() =>
  [0, 1, 2].map((offset) => ({
    item:
      notifications.value[(cursor.value + offset) % notifications.value.length]!,
    workspace: projectNames[(cursor.value + offset) % projectNames.length]!,
    id: cursor.value + offset,
  })),
);
let timer: ReturnType<typeof setInterval> | undefined;
let media: MediaQueryList | undefined;
function preference() {
  reduced.value = media?.matches ?? false;
}
onMounted(() => {
  media = matchMedia("(prefers-reduced-motion: reduce)");
  preference();
  media.addEventListener("change", preference);
  timer = setInterval(() => {
    if (!paused.value && !reduced.value && !document.hidden) cursor.value++;
  }, 3400);
});
onUnmounted(() => {
  clearInterval(timer);
  media?.removeEventListener("change", preference);
});
</script>
<template>
  <section
    class="notification-preview"
    :aria-label="t('preview.ariaLabel')"
  >
    <div class="preview-heading">
      <span>{{ t("preview.heading") }}</span>
      <button
        v-if="!reduced"
        class="preview-control"
        :aria-pressed="paused"
        @click="paused = !paused"
      >
        {{ paused ? t("preview.resume") : t("preview.pause") }}
      </button>
      <button v-else class="preview-control" @click="cursor++">{{ t("preview.next") }}</button>
    </div>
    <TransitionGroup :css="!reduced" name="notification" tag="div" class="notification-stack">
      <article
        v-for="{ item, workspace, id } in visible"
        :key="id"
        class="notification-card"
      >
        <img
          class="notification-brand-logo"
          :src="brandLogo"
          alt="Agent Notifications"
          width="48"
          height="48"
        />
        <div class="notification-copy">
          <div class="notification-title">
            <h3>{{ item.title }}</h3>
            <span>{{ t("common.now") }}</span>
          </div>
          <strong>main · {{ workspace }}</strong>
          <p>{{ item.body }}</p>
          <span v-if="'detail' in item" class="notification-detail">{{
            item.detail
          }}</span>
        </div>
      </article>
    </TransitionGroup>
  </section>
</template>
