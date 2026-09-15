<script setup lang="ts">
import {
  SelectContent,
  SelectIcon,
  SelectItem,
  SelectItemIndicator,
  SelectItemText,
  SelectPortal,
  SelectRoot,
  SelectScrollDownButton,
  SelectScrollUpButton,
  SelectTrigger,
  SelectViewport,
} from "reka-ui";

type SelectOption = {
  value: string;
  label: string;
  icon?: string;
};

const props = defineProps<{
  modelValue: string;
  options: readonly SelectOption[];
  label: string;
  leadingIcon?:
    | "category"
    | "component"
    | "source"
    | "trust"
    | "agent"
    | "authentication"
    | "owner";
}>();

const emit = defineEmits<{
  "update:modelValue": [value: string];
}>();
const { t } = useI18n();

const selected = computed(
  () =>
    props.options.find((option) => option.value === props.modelValue) ??
    props.options[0],
);

watchEffect(() => {
  if (!props.options.length)
    throw new Error(`AppSelect "${props.label}" requires at least one option`);
  if (props.options.some((option) => option.value === ""))
    throw new Error(
      `AppSelect "${props.label}" options must use non-empty values`,
    );
  if (!props.options.some((option) => option.value === props.modelValue))
    throw new Error(`AppSelect "${props.label}" received an unknown value`);
});

function updateValue(value: unknown) {
  if (typeof value === "string") emit("update:modelValue", value);
}
</script>

<template>
  <SelectRoot :model-value="modelValue" @update:model-value="updateValue">
    <SelectTrigger class="app-select__trigger" :aria-label="label">
      <span class="app-select__value">
        <span v-if="selected?.icon" class="app-select__value-icon"
          ><img :src="selected.icon" alt="" width="20" height="20"
        /></span>

        <span>{{ selected?.label }}</span>
      </span>
      <SelectIcon class="app-select__chevron" aria-hidden="true"
        ><svg viewBox="0 0 16 16" fill="none">
          <path d="m3.5 6 4.5 4 4.5-4" /></svg
      ></SelectIcon>
    </SelectTrigger>
    <SelectPortal>
      <SelectContent
        class="app-select__content"
        position="popper"
        align="start"
        :side-offset="7"
      >
        <SelectScrollUpButton
          class="app-select__scroll"
          :aria-label="t('select.scrollUp')"
          >⌃</SelectScrollUpButton
        >
        <SelectViewport class="app-select__viewport">
          <SelectItem
            v-for="option in options"
            :key="option.value"
            class="app-select__item"
            :value="option.value"
          >
            <span v-if="option.icon" class="app-select__item-icon"
              ><img :src="option.icon" alt="" width="20" height="20"
            /></span>
            <SelectItemText>{{ option.label }}</SelectItemText>
            <SelectItemIndicator class="app-select__indicator"
              >✓</SelectItemIndicator
            >
          </SelectItem>
        </SelectViewport>
        <SelectScrollDownButton
          class="app-select__scroll"
          :aria-label="t('select.scrollDown')"
          >⌄</SelectScrollDownButton
        >
      </SelectContent>
    </SelectPortal>
  </SelectRoot>
</template>
