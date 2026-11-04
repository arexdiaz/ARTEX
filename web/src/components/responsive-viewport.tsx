"use client";

import { useEffect } from "react";

// The visual viewport follows the space left by a phone's browser chrome and
// keyboard. Only the phone stylesheet uses this value; desktop sizing is intact.
export function ResponsiveViewport() {
  useEffect(() => {
    let frame = 0;
    const update = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        const height = Math.round(window.visualViewport?.height || window.innerHeight);
        document.documentElement.style.setProperty("--app-viewport-height", `${height}px`);
      });
    };

    update();
    window.visualViewport?.addEventListener("resize", update);
    window.addEventListener("resize", update);
    return () => {
      cancelAnimationFrame(frame);
      window.visualViewport?.removeEventListener("resize", update);
      window.removeEventListener("resize", update);
      document.documentElement.style.removeProperty("--app-viewport-height");
    };
  }, []);

  return null;
}
