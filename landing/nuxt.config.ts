import { supportedLocales } from "./data/i18n";

const baseURL = process.env.NUXT_APP_BASE_URL || "/agent-notifications/";
const siteUrl =
  process.env.NUXT_PUBLIC_SITE_URL ||
  "https://777genius.github.io/agent-notifications";
const siteOrigin = new URL(siteUrl).origin;

export default defineNuxtConfig({
  compatibilityDate: "2026-09-09",
  devtools: { enabled: false },
  app: {
    baseURL,
    head: {
      title: "Agent Notifications",
    },
  },
  modules: ["@nuxtjs/i18n"],
  css: ["~/assets/main.css"],
  nitro: {
    preset: "static",
    prerender: { routes: ["/", ...supportedLocales.filter((locale) => locale.code !== "en").map((locale) => `/${locale.code}/`)] },
  },
  i18n: {
    restructureDir: ".",
    locales: [...supportedLocales],
    defaultLocale: "en",
    strategy: "prefix_except_default",
    langDir: "locales",
    baseUrl: siteOrigin,
    detectBrowserLanguage: {
      useCookie: true,
      cookieKey: "agent_notifications_locale",
      redirectOn: "root",
      alwaysRedirect: false,
      fallbackLocale: "en",
    },
  },
});
