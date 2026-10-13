<script setup lang="ts">
const root = ref<HTMLElement>();
let media: MediaQueryList | undefined;
let surface: HTMLElement | undefined;
let frame: number | undefined;
let pointerX = 0;
let pointerY = 0;

function paint() {
  frame = undefined;
  const reduced = media?.matches ?? false;
  root.value?.style.setProperty("--parallax-x", `${reduced ? 0 : pointerX}px`);
  root.value?.style.setProperty("--parallax-y", `${reduced ? 0 : pointerY}px`);
  root.value?.style.setProperty("--scroll-shift", `${reduced ? 0 : window.scrollY * 0.14}px`);
}

function schedule() {
  if (frame === undefined) frame = requestAnimationFrame(paint);
}

function updatePreference() {
  pointerX = 0;
  pointerY = 0;
  schedule();
}

function move(event: PointerEvent) {
  if (media?.matches || event.pointerType !== "mouse") return;
  pointerX = Math.round((event.clientX / window.innerWidth - 0.5) * 96);
  pointerY = Math.round((event.clientY / window.innerHeight - 0.5) * 64);
  schedule();
}

function resetPointer() {
  pointerX = 0;
  pointerY = 0;
  schedule();
}

function scroll() {
  if (!media?.matches) schedule();
}

onMounted(() => {
  media = matchMedia("(prefers-reduced-motion: reduce)");
  surface = root.value?.parentElement ?? undefined;
  updatePreference();
  media.addEventListener("change", updatePreference);
  surface?.addEventListener("pointermove", move, { passive: true });
  surface?.addEventListener("pointerleave", resetPointer);
  window.addEventListener("scroll", scroll, { passive: true });
});
onUnmounted(() => {
  media?.removeEventListener("change", updatePreference);
  surface?.removeEventListener("pointermove", move);
  surface?.removeEventListener("pointerleave", resetPointer);
  window.removeEventListener("scroll", scroll);
  if (frame !== undefined) cancelAnimationFrame(frame);
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
  z-index: -1;
  overflow: hidden;
  --parallax-x: 0px;
  --parallax-y: 0px;
  --scroll-shift: 0px;
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
  background-position: calc(var(--parallax-x) * -0.55)
    calc(var(--parallax-y) * -0.55 + var(--scroll-shift) * 0.45);
  transition: background-position 180ms ease-out;
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
  translate: var(--parallax-x) calc(var(--parallax-y) + var(--scroll-shift) * var(--depth, 1));
  transition: translate 220ms ease-out;
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
  --depth: 0.65;
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
  --depth: 0.7;
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
  --depth: 0.6;
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
    transform: translate(0, 0);
  }
  50% {
    transform: translate(-30px, 20px);
  }
}

@keyframes orbDrift2 {
  0%,
  100% {
    transform: translate(0, 0);
  }
  50% {
    transform: translate(25px, -15px);
  }
}

@media (prefers-reduced-motion: reduce) {
  .page-bg__orb {
    animation: none !important;
    translate: none;
  }
}
</style>
