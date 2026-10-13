import type { Ref } from "vue";

// Content stays visible during SSR and when JavaScript or IntersectionObserver is unavailable.
export function useScrollReveal(root: Ref<HTMLElement | undefined>) {
  let observer: IntersectionObserver | undefined;
  let media: MediaQueryList | undefined;

  function reveal(element: Element) {
    element.classList.remove("reveal-pending");
    observer?.unobserve(element);
  }

  function updatePreference() {
    if (!media?.matches) return;
    root.value?.querySelectorAll(".reveal-pending").forEach(reveal);
    observer?.disconnect();
  }

  function focus(event: FocusEvent) {
    if (event.target instanceof Element) {
      const element = event.target.closest(".reveal-pending");
      if (element) reveal(element);
    }
  }

  onMounted(() => {
    media = matchMedia("(prefers-reduced-motion: reduce)");
    if (!("IntersectionObserver" in window)) return;
    media.addEventListener("change", updatePreference);
    root.value?.addEventListener("focusin", focus);
    if (media.matches) return;
    observer = new IntersectionObserver((entries) => {
      entries.forEach((entry) => {
        if (entry.isIntersecting) reveal(entry.target);
      });
    }, { rootMargin: "0px 0px -32px 0px" });
    root.value?.querySelectorAll<HTMLElement>("[data-reveal]").forEach((element) => {
      // Leave the initial viewport and hash destinations visible immediately.
      if (element.getBoundingClientRect().top < window.innerHeight) return;
      element.classList.add("reveal-pending");
      observer?.observe(element);
    });
  });

  onUnmounted(() => {
    observer?.disconnect();
    media?.removeEventListener("change", updatePreference);
    root.value?.removeEventListener("focusin", focus);
  });
}
