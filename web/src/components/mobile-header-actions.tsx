"use client";

import { type ReactNode, useEffect, useState } from "react";
import { createPortal } from "react-dom";

import { useIsMobile } from "@/hooks/use-mobile";

// Pages can place phone controls beside the navigation toggle without adding
// another toolbar or changing the desktop header.
export function MobileHeaderActions({ children }: { children: ReactNode }) {
  const isMobile = useIsMobile();
  const [host, setHost] = useState<HTMLElement | null>(null);

  useEffect(() => {
    setHost(isMobile ? document.getElementById("mobile-header-actions") : null);
  }, [isMobile]);

  return isMobile && host ? createPortal(children, host) : null;
}
