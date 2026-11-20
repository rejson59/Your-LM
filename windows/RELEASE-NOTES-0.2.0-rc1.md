## YourLM 0.2.0-rc1 — nowa baza dla Lenovo + Redmi

To wydanie zastępuje wcześniejsze paczki testowe i rozpoczyna linię łatwych aktualizacji.

### Najważniejsze zmiany

- **Jeden plik Windows:** pobierz i uruchom `YourLM.exe`. Nie ma ZIP-a ani katalogu runtime do pilnowania.
- **Instalacja telefonu przez USB:** nowa karta **Telefon USB** wykrywa ADB i instaluje/uaktualnia dołączone APK przez `adb install -r`, zachowując dane aplikacji.
- **Aktualizacje Windows i Android:** obie aplikacje sprawdzają GitHub Releases. Windows pobiera i podmienia EXE; Android przekazuje pobrane APK do systemowego ekranu potwierdzenia.
- **Stała tożsamość Androida:** od tego wydania kolejne oficjalne APK będą podpisywane tym samym kluczem aktualizacji.
- **Odświeżony interfejs:** czytelniejszy pulpit, onboarding USB, statusy urządzeń i centrum aktualizacji.
- Nadal dostępne są: lokalny czat/API OpenAI, host Android, worker Android/Windows, parowanie TLS oraz podział warstw GGUF.

### Co pobrać

- `YourLM.exe` — Windows 10/11 x64, samodzielny plik.
- `YourLM-Android-arm64.apk` — Android 9+ ARM64 (m.in. Redmi).
- `SHA256SUMS` — sumy kontrolne obu plików.

### Pierwszy test na Redmi

1. Uruchom EXE jako zwykły użytkownik.
2. Otwórz **Telefon USB**.
3. W Redmi włącz Opcje programistyczne, Debugowanie USB i — jeśli HyperOS tego wymaga — **Instalowanie przez USB**.
4. Podłącz przewód z transmisją danych, zaakceptuj odcisk RSA i kliknij **Zainstaluj YourLM**.
5. Najpierw sprawdź mały model 3–4B Q4_K_M na samym laptopie, potem dołącz telefon jako worker i porównaj wynik.

### Ważne dla wersji RC

To kandydat do testów na fizycznym Lenovo/Redmi. EXE nie ma komercyjnego podpisu Authenticode. Nie wyłączaj Defendera ani nie dodawaj wyjątku; jeśli pojawi się wykrycie, zgłoś pełną nazwę i SHA-256. Android wymaga potwierdzenia każdej instalacji/aktualizacji na ekranie systemowym.

Modele GGUF nie są dołączone i pozostają lokalnie. Aktualizacja nie usuwa modeli ani ustawień.
