import { multicaCookieName } from "@multica/core/cookies";

export function setLoggedInCookie() {
  document.cookie = `${multicaCookieName("loggedIn")}=1; path=/; max-age=31536000; samesite=lax`;
}

export function clearLoggedInCookie() {
  document.cookie = `${multicaCookieName("loggedIn")}=; path=/; max-age=0`;
}
