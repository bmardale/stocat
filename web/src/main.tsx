import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "@tanstack/react-router";
import { createAppRouter } from "./router";
import "./style.css";

const container = document.getElementById("app");

if (!container) {
  throw new Error("Missing application container");
}

const router = createAppRouter();

createRoot(container).render(
  <StrictMode>
    <RouterProvider router={router} />
  </StrictMode>,
);
