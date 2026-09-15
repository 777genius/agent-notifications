<script setup lang="ts">
const base = useRuntimeConfig().app.baseURL;
const platforms = [{icon: 'apple', label: 'macOS'}, {icon: 'linux', label: 'Linux'}, {icon: 'windows', label: 'Windows'}];
</script>
<template><span class="platform-logos"><img v-for="platform in platforms" :key="platform.icon" :src="`${base}platforms/${platform.icon}.svg`" :alt="platform.label" :title="platform.label" width="24" height="24" /></span></template>
