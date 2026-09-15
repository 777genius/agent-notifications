<script setup lang="ts">
const root = ref<HTMLElement>();
const reduced = ref(false);
let media: MediaQueryList | undefined;
let frame: number | undefined;

function reset() {
  root.value?.style.setProperty("--parallax-x", "0px");
  root.value?.style.setProperty("--parallax-y", "0px");
}

function updatePreference() {
  reduced.value = media?.matches ?? false;
  if (reduced.value) reset();
}

function move(event: PointerEvent) {
  const hero = root.value?.parentElement;
  if (reduced.value || !hero) return;
  const bounds = hero.getBoundingClientRect();
  const x = ((event.clientX - bounds.left) / bounds.width - 0.5) * 2;
  const y = ((event.clientY - bounds.top) / bounds.height - 0.5) * 2;
  cancelAnimationFrame(frame ?? 0);
  frame = requestAnimationFrame(() => {
    root.value?.style.setProperty("--parallax-x", `${Math.round(x * 14)}px`);
    root.value?.style.setProperty("--parallax-y", `${Math.round(y * 10)}px`);
  });
}

onMounted(() => {
  media = matchMedia("(prefers-reduced-motion: reduce)");
  updatePreference();
  media.addEventListener("change", updatePreference);
  root.value?.parentElement?.addEventListener("pointermove", move);
  root.value?.parentElement?.addEventListener("pointerleave", reset);
});
onUnmounted(() => {
  media?.removeEventListener("change", updatePreference);
  root.value?.parentElement?.removeEventListener("pointermove", move);
  root.value?.parentElement?.removeEventListener("pointerleave", reset);
  cancelAnimationFrame(frame ?? 0);
});
</script>

<template>
  <div ref="root" class="page-bg" aria-hidden="true">
    <div class="page-bg__grid" />
    <div class="page-bg__orb page-bg__orb--1" />
    <div class="page-bg__orb page-bg__orb--2" />
    <div class="page-bg__orb page-bg__orb--3" />
    <div class="page-bg__orb page-bg__orb--4" />
    <div class="page-bg__orb page-bg__orb--5" />
    <div class="page-bg__orb page-bg__orb--6" />
    <div class="page-bg__orb page-bg__orb--7" />
    <div class="page-bg__scanline" />
  </div>
</template>

<style scoped>
.page-bg {
  position: absolute;
  inset: 0;
  pointer-events: none;
  z-index: 0;
  overflow: hidden;
  --parallax-x: 0px;
  --parallax-y: 0px;
  transition: --parallax-x 160ms ease-out, --parallax-y 160ms ease-out;
}

/* Grid overlay */
.page-bg__grid {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(0, 240, 255, 0.03) 1px, transparent 1px),
    linear-gradient(90deg, rgba(0, 240, 255, 0.03) 1px, transparent 1px);
  background-size: 60px 60px;
  z-index: 1;
  transform: translate3d(calc(var(--parallax-x) * -0.35), calc(var(--parallax-y) * -0.35), 0);
}

/* Scanline effect */
.page-bg__scanline {
  position: absolute;
  inset: 0;
  background: repeating-linear-gradient(
    0deg,
    transparent,
    transparent 2px,
    rgba(0, 240, 255, 0.008) 2px,
    rgba(0, 240, 255, 0.008) 4px
  );
  z-index: 2;
}

.page-bg__orb {
  position: absolute;
  border-radius: 50%;
  filter: blur(140px);
  opacity: 0.08;
}

.page-bg__orb--1 {
  width: 900px;
  height: 900px;
  background: #00f0ff;
  top: -200px;
  right: -150px;
  animation: orbDrift1 20s ease-in-out infinite;
}

.page-bg__orb--2 {
  width: 700px;
  height: 700px;
  background: #ff00ff;
  top: 300px;
  left: -200px;
  animation: orbDrift2 25s ease-in-out infinite;
}

.page-bg__orb--3 {
  width: 800px;
  height: 800px;
  background: #39ff14;
  top: 1200px;
  right: -100px;
  opacity: 0.05;
  animation: orbDrift1 22s ease-in-out infinite;
}

.page-bg__orb--4 {
  width: 700px;
  height: 700px;
  background: #00f0ff;
  top: 2100px;
  left: -150px;
  opacity: 0.06;
  animation: orbDrift2 18s ease-in-out infinite;
}

.page-bg__orb--5 {
  width: 750px;
  height: 750px;
  background: #ff00ff;
  top: 2900px;
  right: -120px;
  opacity: 0.05;
  animation: orbDrift1 24s ease-in-out infinite;
}

.page-bg__orb--6 {
  width: 700px;
  height: 700px;
  background: #ffd700;
  top: 3600px;
  left: -100px;
  opacity: 0.04;
  animation: orbDrift2 20s ease-in-out infinite;
}

.page-bg__orb--7 {
  width: 650px;
  height: 650px;
  background: #00f0ff;
  top: 4300px;
  right: -80px;
  opacity: 0.05;
  animation: orbDrift1 17s ease-in-out infinite;
}

@keyframes orbDrift1 {
  0%,
  100% {
    transform: translate(var(--parallax-x), var(--parallax-y));
  }
  50% {
    transform: translate(calc(var(--parallax-x) - 30px), calc(var(--parallax-y) + 20px));
  }
}

@keyframes orbDrift2 {
  0%,
  100% {
    transform: translate(var(--parallax-x), var(--parallax-y));
  }
  50% {
    transform: translate(calc(var(--parallax-x) + 25px), calc(var(--parallax-y) - 15px));
  }
}

@media (prefers-reduced-motion: reduce) {
  .page-bg__orb {
    animation: none !important;
  }
}
</style>
