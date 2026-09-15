<script setup lang="ts">
import { isLocaleCode, supportedLocales, type LocaleCode } from "~/data/i18n";

const { locale, setLocale, t, te } = useI18n();
const base = useRuntimeConfig().app.baseURL;
const root = ref<HTMLElement>();
const searchInput = ref<HTMLInputElement>();
const open = ref(false);
const query = ref("");
const pending = ref(false);
const error = ref(false);

const currentLocale = computed(
  () =>
    supportedLocales.find((item) => item.code === locale.value) ??
    supportedLocales[0],
);
const filteredLocales = computed(() => {
  const needle = query.value.trim().toLocaleLowerCase();
  if (!needle) return supportedLocales;
  return supportedLocales.filter((item) =>
    `${item.name} ${item.code} ${item.language}`
      .toLocaleLowerCase()
      .includes(needle),
  );
});

function toggle() {
  if (pending.value) return;
  open.value = !open.value;
  error.value = false;
}

function close() {
  open.value = false;
  query.value = "";
}

async function selectLanguage(value: LocaleCode) {
  if (!isLocaleCode(value) || value === locale.value || pending.value) {
    close();
    return;
  }

  pending.value = true;
  error.value = false;
  const previousLocale = locale.value;
  try {
    await setLocale(value);
    if (locale.value !== value || !te("seo.title", value))
      throw new Error(`Locale ${value} was not activated`);
    close();
  } catch {
    if (locale.value !== previousLocale) {
      try {
        await setLocale(previousLocale);
      } catch {
        // Keep the alert visible if rollback also fails.
      }
    }
    error.value = true;
  } finally {
    pending.value = false;
  }
}

function focusFirstOption() {
  root.value?.querySelector<HTMLButtonElement>("[role='option']")?.focus();
}

function onDocumentPointerDown(event: PointerEvent) {
  if (!root.value?.contains(event.target as Node)) close();
}

watch(open, async (isOpen) => {
  if (!isOpen) return;
  await nextTick();
  searchInput.value?.focus();
});

onMounted(() => document.addEventListener("pointerdown", onDocumentPointerDown));
onBeforeUnmount(() =>
  document.removeEventListener("pointerdown", onDocumentPointerDown),
);
</script>

<template>
  <div ref="root" class="language-switcher">
    <button
      class="language-switcher__trigger"
      type="button"
      :aria-label="t('language.current', { language: currentLocale.name })"
      aria-haspopup="listbox"
      :aria-expanded="open"
      aria-controls="language-options"
      :disabled="pending"
      :aria-busy="pending"
      @click="toggle"
      @keydown.down.prevent="open = true"
      @keydown.esc="close"
    >
      <img
        class="language-switcher__flag"
        :src="`${base}flags/${currentLocale.flag}.svg`"
        alt=""
        aria-hidden="true"
        width="20"
        height="15"
        loading="eager"
      />
      <span>{{ currentLocale.name }}</span>
      <svg class="language-switcher__chevron" viewBox="0 0 12 8" aria-hidden="true">
        <path d="m1 1 5 5 5-5" />
      </svg>
    </button>

    <div v-if="open" class="language-switcher__popover">
      <label class="language-switcher__search">
        <span class="visually-hidden">{{ t("language.search") }}</span>
        <svg viewBox="0 0 20 20" aria-hidden="true">
          <circle cx="8.5" cy="8.5" r="5.5" />
          <path d="m13 13 4 4" />
        </svg>
        <input
          ref="searchInput"
          v-model="query"
          type="search"
          autocomplete="off"
          :placeholder="t('language.search')"
          @keydown.esc.prevent="close"
          @keydown.down.prevent="focusFirstOption"
        />
      </label>

      <div id="language-options" class="language-switcher__options" role="listbox" :aria-label="t('language.label')">
        <button
          v-for="item in filteredLocales"
          :key="item.code"
          type="button"
          role="option"
          :aria-selected="item.code === locale"
          :class="{ active: item.code === locale }"
          @click="selectLanguage(item.code)"
        >
          <img
            class="language-switcher__flag"
            :src="`${base}flags/${item.flag}.svg`"
            alt=""
            aria-hidden="true"
            width="20"
            height="15"
            loading="lazy"
          />
          <span>{{ item.name }}</span>
          <svg v-if="item.code === locale" viewBox="0 0 16 16" aria-hidden="true">
            <path d="m3 8 3 3 7-7" />
          </svg>
        </button>
        <p v-if="filteredLocales.length === 0" class="language-switcher__empty">
          {{ t("language.noResults") }}
        </p>
      </div>
    </div>

    <span v-if="error" class="visually-hidden" role="alert">
      {{ t("language.switchError") }}
    </span>
  </div>
</template>
