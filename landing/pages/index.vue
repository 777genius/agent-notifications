<script setup lang="ts">
import { repo } from "~/data/install";

const { t } = useI18n();
const localeHead = useLocaleHead({ seo: true });
const base = useRuntimeConfig().app.baseURL;
const brandLogo = `${base}agent-notifications-logo.png`;
const publicBrandLogo =
  "https://777genius.github.io/agent-notifications/agent-notifications-logo.png";

useHead(() => ({
  title: t("seo.title"),
  htmlAttrs: localeHead.value.htmlAttrs,
  link: [
    ...(localeHead.value.link ?? []),
    { rel: "icon", type: "image/png", href: brandLogo },
    { rel: "apple-touch-icon", href: brandLogo },
    { rel: "manifest", href: `${base}site.webmanifest` },
  ],
  meta: [
    ...(localeHead.value.meta ?? []),
    { name: "description", content: t("seo.description") },
    { property: "og:title", content: t("seo.title") },
    { property: "og:description", content: t("seo.description") },
    { property: "og:type", content: "website" },
    { property: "og:site_name", content: "Agent Notifications" },
    { property: "og:image", content: publicBrandLogo },
    { name: "twitter:card", content: "summary" },
    { name: "twitter:title", content: t("seo.title") },
    { name: "twitter:description", content: t("seo.description") },
    { name: "twitter:image", content: publicBrandLogo },
    { name: "theme-color", content: "#080b12" },
  ],
  script: [
    {
      type: "application/ld+json",
      innerHTML: JSON.stringify({
        "@context": "https://schema.org",
        "@type": "SoftwareApplication",
        name: "Agent Notifications",
        description: t("seo.description"),
        applicationCategory: "DeveloperApplication",
        operatingSystem: "macOS, Linux, Windows",
        isAccessibleForFree: true,
        offers: {
          "@type": "Offer",
          price: "0",
          priceCurrency: "USD",
        },
        url: "https://777genius.github.io/agent-notifications/",
        image: publicBrandLogo,
        downloadUrl: repo,
      }),
    },
  ],
}));

const features = computed(() =>
  [1, 2, 3].map((number) => ({
    number: `0${number}`,
    title: t(`features.items.${number}.title`),
    text: t(`features.items.${number}.text`),
    label: t(`features.items.${number}.label`),
  })),
);

const faqIcons = [
  "M8 3v3m8-3v3M5 6h14v14H5zM9 11h.01M15 11h.01M9 16h6",
  "M8 4H4v6h4m8 4h4v6h-4M4 7h10a4 4 0 0 1 4 4v3M6 10v3a4 4 0 0 0 4 4h10",
  "M20 7v5h-5M4 17v-5h5M6 7a7 7 0 0 1 12-1l2 6M4 12l2 6a7 7 0 0 0 12-1",
  "M4 9h4l5-4v14l-5-4H4zM16 8a6 6 0 0 1 0 8M19 5a10 10 0 0 1 0 14",
  "M4 4h16v12H4zM8 20h8m-4-4v4",
  "M9 9a3 3 0 1 1 5 2c-2 1-2 2-2 3m0 3h.01M22 12a10 10 0 1 1-20 0 10 10 0 0 1 20 0",
];
</script>

<template>
  <div class="site">
    <a class="skip" href="#main">{{ t("accessibility.skip") }}</a>
    <header class="header">
      <a class="brand" href="#">
        <img class="brand-icon" :src="brandLogo" alt="" width="38" height="38" />
        <span>Agent Notifications<small>{{ t("brand.tagline") }}</small></span>
      </a>
      <nav :aria-label="t('nav.ariaLabel')">
        <a href="#features">{{ t("nav.features") }}</a>
        <a href="#install">{{ t("nav.install") }}</a>
        <a href="#faq">{{ t("nav.faq") }}</a>
      </nav>
      <div class="header-actions">
        <LanguageSwitcher />
        <a class="github" :href="repo">GitHub ↗</a>
      </div>
    </header>

    <main id="main">
      <div class="hero-wrap">
        <PageBackground />
        <section class="hero">
          <div class="hero-copy">
            <p class="eyebrow"><span class="dot" /> {{ t("hero.eyebrow") }}</p>
            <h1>{{ t("hero.title") }}<br /><em>{{ t("hero.emphasis") }}</em></h1>
            <p class="lead">
              {{ t("hero.leadFirst") }}<br />{{ t("hero.leadSecond") }}
            </p>
            <div class="hero-actions">
              <a class="primary" href="#install">{{ t("hero.primary") }} <span>↗</span></a>
              <a class="secondary" :href="repo">{{ t("hero.secondary") }}</a>
            </div>
            <p class="hero-note">{{ t("hero.note") }}</p>
          </div>
          <NotificationPreview />
        </section>
      </div>

      <div class="compatibility" role="group" :aria-label="t('accessibility.platforms')">
        <PlatformLogos />
      </div>
      <InstallWizard />

      <section id="features" class="section anchor-offset">
        <p class="eyebrow">{{ t("features.eyebrow") }}</p>
        <h2>{{ t("features.titleFirst") }}<br />{{ t("features.titleSecond") }}</h2>
        <div class="feature-grid">
          <article v-for="feature in features" :key="feature.number" class="panel feature">
            <div class="feature-visual" aria-hidden="true">
              <div v-if="feature.number === '01'" class="mini-notification">
                <span class="signal-icon">✓</span>
                <div><strong>{{ t("features.preview.done") }}</strong><span>{{ t("features.preview.ready") }}</span></div>
                <span class="mini-now">{{ t("common.now") }}</span>
              </div>
              <div v-else-if="feature.number === '02'" class="sound-preview">
                <div class="sound-wave"><i v-for="(height, index) in [12, 22, 36, 20, 46, 30, 54, 38, 24, 44, 28, 16, 32, 20, 10]" :key="index" :style="{ height: height + 'px' }" /></div>
                <span>{{ t("features.preview.sound") }}</span>
              </div>
              <div v-else class="channel-preview">
                <span class="channel-source">{{ t("features.preview.event") }}</span>
                <span class="channel-line" />
                <div class="channel-tags"><span>Slack</span><span>Discord</span><span>Telegram</span><span>Teams</span></div>
              </div>
            </div>
            <div class="feature-top"><span>{{ feature.label }}</span></div>
            <h3>{{ feature.title }}</h3>
            <p>{{ feature.text }}</p>
          </article>
        </div>
        <p class="feature-foot">
          {{ t("features.foot") }}
          <a :href="repo + '#platform-support'">{{ t("features.details") }} ↗</a>
        </p>
      </section>

      <TerminalSupportTable />

      <section id="faq" class="section faq anchor-offset">
        <div class="faq-header">
          <p class="eyebrow">{{ t("faq.eyebrow") }}</p>
          <h2>{{ t("faq.title") }}</h2>
          <p>{{ t("faq.subtitle") }}</p>
        </div>
        <div class="faq-content">
          <div class="faq-list">
            <details v-for="index in 6" :key="index">
              <summary>
                <span class="faq-icon" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path :d="faqIcons[index - 1]" /></svg></span>
                <span>{{ t(`faq.items.${index}.question`) }}</span>
                <span class="faq-chevron" aria-hidden="true" />
              </summary>
              <p>
                {{ t(`faq.items.${index}.answer`) }}
                <a v-if="index === 5" :href="repo + '#platform-support'">{{ t("faq.items.5.link") }}</a>
                <a v-if="index === 6" :href="repo + '#troubleshooting'">{{ t("faq.items.6.link") }}</a>
              </p>
            </details>
          </div>
          <div class="faq-decoration" aria-hidden="true"><span class="faq-ring" /><span class="faq-ring" /><span class="faq-ring" /><span class="faq-orb">?</span></div>
        </div>
      </section>

      <section class="closing">
        <p class="eyebrow">{{ t("closing.eyebrow") }}</p>
        <h2>{{ t("closing.title") }}</h2>
        <a class="primary" href="#install">{{ t("closing.action") }} ↗</a>
      </section>
    </main>

    <footer>
      <a class="brand" href="#"><img class="brand-icon" :src="brandLogo" alt="" width="32" height="32" />Agent Notifications</a>
      <span>{{ t("footer.builtBy") }} <a href="https://github.com/777genius">777genius</a>{{ t("footer.afterBuilder") }}</span>
      <a :href="repo + '/blob/main/LICENSE'">{{ t("footer.license") }}</a>
      <a :href="repo">{{ t("footer.source") }} ↗</a>
    </footer>
  </div>
</template>
