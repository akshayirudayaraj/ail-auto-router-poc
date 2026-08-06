import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import "./styles.css";
import { App } from "./App";
import { ConsoleProvider } from "./store";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      <ConsoleProvider>
        <App />
      </ConsoleProvider>
    </BrowserRouter>
  </StrictMode>,
);
