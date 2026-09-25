<script setup lang="ts">
const props = defineProps<{ only?: "apple" | "linux" | "windows"; size?: number }>();
const base = useRuntimeConfig().app.baseURL;
const allPlatforms = [{icon: 'apple', label: 'macOS'}, {icon: 'linux', label: 'Linux'}, {icon: 'windows', label: 'Windows'}];
const platforms = computed(() => props.only ? allPlatforms.filter((p) => p.icon === props.only) : allPlatforms);
const size = computed(() => props.size ?? 24);
</script>
<template><span class="platform-logos"><img v-for="platform in platforms" :key="platform.icon" :src="`${base}platforms/${platform.icon}.svg`" :alt="platform.label" :title="platform.label" :width="size" :height="size" /></span></template>
