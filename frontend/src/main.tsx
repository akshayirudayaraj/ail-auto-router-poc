import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./styles.css";
import { App } from "./App";
import { ConsoleProvider } from "./store";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ConsoleProvider>
      <App />
    </ConsoleProvider>
  </StrictMode>,
);
