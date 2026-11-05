"use client";

import { useEffect } from "react";

// Follow both the size and the origin of the visible viewport. Mobile browsers
// can pan it when the keyboard opens without resizing the layout viewport.
export function ResponsiveViewport() {
  useEffect(() => {
    let frame = 0;
    const root = document.documentElement;
    const viewport = window.visualViewport;
    const update = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        // Pinch zoom should magnify the existing layout, not make it reflow into
        // the smaller visual viewport as if a keyboard had opened.
        const zoomed = viewport && Math.abs(viewport.scale - 1) > 0.01;
        const height = !zoomed && viewport ? viewport.height : window.innerHeight;
        const width = !zoomed && viewport ? viewport.width : root.clientWidth;
        const top = !zoomed && viewport ? Math.max(0, viewport.offsetTop) : 0;
        const left = !zoomed && viewport ? Math.max(0, viewport.offsetLeft) : 0;
        root.style.setProperty("--app-viewport-height", `${height}px`);
        root.style.setProperty("--app-viewport-width", `${width}px`);
        root.style.setProperty("--app-viewport-top", `${top}px`);
        root.style.setProperty("--app-viewport-left", `${left}px`);
        // Safari can retain the home-indicator inset above an open keyboard.
        // The visible viewport already excludes the keyboard; do not add that
        // bottom inset a second time. Resize-both browsers keep their native inset.
        if (!zoomed && window.innerHeight - height > 100) {
          root.style.setProperty("--app-keyboard-safe-area-bottom", "0px");
        } else {
          root.style.removeProperty("--app-keyboard-safe-area-bottom");
        }
      });
    };

    update();
    viewport?.addEventListener("resize", update);
    viewport?.addEventListener("scroll", update);
    window.addEventListener("resize", update);
    window.addEventListener("scroll", update, { passive: true });
    window.addEventListener("pageshow", update);
    return () => {
      cancelAnimationFrame(frame);
      viewport?.removeEventListener("resize", update);
      viewport?.removeEventListener("scroll", update);
      window.removeEventListener("resize", update);
      window.removeEventListener("scroll", update);
      window.removeEventListener("pageshow", update);
      for (const property of ["height", "width", "top", "left"]) {
        root.style.removeProperty(`--app-viewport-${property}`);
      }
      root.style.removeProperty("--app-keyboard-safe-area-bottom");
    };
  }, []);

  return null;
}
