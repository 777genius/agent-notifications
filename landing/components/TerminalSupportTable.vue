<script setup lang="ts">
import { terminalSupport, focusLevelIcon, type FocusLevel } from "~/data/terminalSupport";
import { repo } from "~/data/install";

const { t } = useI18n();
const base = useRuntimeConfig().app.baseURL;

const rows = computed(() =>
  terminalSupport.map((row) => ({
    id: row.id,
    name: t(`terminalSupport.names.${row.nameKey}`),
    logoSrc: `${base}terminals/${row.logo}`,
    macos: row.macos,
    linux: row.linux,
    windows: row.windows,
    notes: (row.noteKeys ?? []).map((key) => t(`terminalSupport.notes.${key}`)),
  })),
);

function levelLabel(level: FocusLevel): string {
  return t(`terminalSupport.levels.${level}`);
}
</script>

<template>
  <section id="terminals" class="section anchor-offset terminal-support">
    <p class="eyebrow">{{ t("terminalSupport.eyebrow") }}</p>
    <h2>{{ t("terminalSupport.titleFirst") }}<br /><em>{{ t("terminalSupport.titleSecond") }}</em></h2>
    <p class="terminal-support-intro">{{ t("terminalSupport.intro") }}</p>

    <div class="terminal-table-scroll">
      <table class="terminal-table">
        <thead>
          <tr>
            <th scope="col">{{ t("terminalSupport.columns.app") }}</th>
            <th scope="col"><PlatformLogos only="apple" :size="13" class="os-header-icon" />{{ t("terminalSupport.columns.macos") }}</th>
            <th scope="col"><PlatformLogos only="linux" :size="13" class="os-header-icon" />{{ t("terminalSupport.columns.linux") }}</th>
            <th scope="col"><PlatformLogos only="windows" :size="13" class="os-header-icon" />{{ t("terminalSupport.columns.windows") }}</th>
            <th scope="col">{{ t("terminalSupport.columns.notes") }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.id">
            <th scope="row" class="terminal-table-app">
              <span class="terminal-table-icon"><img :src="row.logoSrc" :alt="row.name" width="16" height="16" loading="lazy" /></span>
              <span>{{ row.name }}</span>
            </th>
            <td :class="`level-${row.macos}`">
              <span class="level-dot" aria-hidden="true">{{ focusLevelIcon[row.macos] }}</span>
              {{ levelLabel(row.macos) }}
            </td>
            <td :class="`level-${row.linux}`">
              <span class="level-dot" aria-hidden="true">{{ focusLevelIcon[row.linux] }}</span>
              {{ levelLabel(row.linux) }}
            </td>
            <td :class="`level-${row.windows}`">
              <span class="level-dot" aria-hidden="true">{{ focusLevelIcon[row.windows] }}</span>
              {{ levelLabel(row.windows) }}
            </td>
            <td class="terminal-table-notes">
              <span v-for="note in row.notes" :key="note">{{ note }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <p class="terminal-support-legend">{{ t("terminalSupport.legend") }}</p>
    <p class="terminal-support-legend">{{ t("terminalSupport.windowsNote") }}</p>
    <p class="terminal-support-legend">{{ t("terminalSupport.fallbackNote") }}</p>
    <p class="feature-foot">
      <a :href="repo + '#platform-support'">{{ t("terminalSupport.detailsLink") }} ↗</a>
    </p>
  </section>
</template>
