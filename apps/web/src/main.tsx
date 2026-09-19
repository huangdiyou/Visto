import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "@review-studio/design-tokens/styles.css";
import "./styles.css";
import { App } from "./App";

const root = document.getElementById("root");

if (!root) {
  throw new Error("Root element was not found.");
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
