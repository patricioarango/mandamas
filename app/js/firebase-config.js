// Firebase web config, used only for Google sign-in. These values are public by design (they identify
// the project, they don't grant access); the Go API checks every request. Fill in from
// Firebase console → Project settings → Your apps.
export const firebaseConfig = {
  apiKey: "AIzaSyD1ZzquhMy4GDyuL1R72BQEUMCjonLc398",
  authDomain: "mandamas.firebaseapp.com",
  projectId: "mandamas",
  storageBucket: "mandamas.firebasestorage.app",
  messagingSenderId: "969984914963",
  appId: "1:969984914963:web:8ebf0da4064e60629c71e6"
};


// Local dev against the Auth emulator: open the app with ?emu on localhost.
export const useAuthEmulator = location.hostname === "localhost" && new URLSearchParams(location.search).has("emu");
