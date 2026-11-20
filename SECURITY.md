# Bezpieczeństwo YourLM

## Co wykonuje wydanie Windows

`YourLM.exe` uruchamia lokalny panel HTTP wyłącznie na losowym porcie `127.0.0.1`, otwiera ten adres przez systemowe `ShellExecuteW`, a po wyborze modelu uruchamia dołączony `runtime/llama-server.exe`. Opcjonalny tryb workera uruchamia `runtime/ggml-rpc-server.exe` tylko na loopbacku; do sieci prywatnej wystawiany jest oddzielny tunel TLS z odciskiem certyfikatu i sekretem przekazywanym w QR.

Kontroler **nie ma aktualizatora, nie pobiera ani nie rozpakowuje programów, nie używa PowerShella lub `rundll32`, nie instaluje usługi, nie zmienia rejestru, nie dodaje autostartu, nie prosi o uprawnienia administratora i nie dodaje wyjątków antywirusowych**. Programy runtime są zwykłymi plikami w ZIP-ie. Ich SHA-256 jest zaszyty w kontrolerze i sprawdzany bezpośrednio przed każdym uruchomieniem. Zmieniony albo brakujący plik jest odrzucany.

Kontroler usuwa ze środowiska procesu potomnego zmienne `LLAMA_*` i `GGML_*`, które mogłyby włączyć inne zachowanie. Jawnie uruchamia `llama-server` w trybie offline, bez Web UI, MCP proxy i wbudowanych narzędzi plikowych/powłoki. Narzędzia agenta Continue działają po stronie Continue i nie wymagają serwerowych narzędzi `llama.cpp`.

Kontroler łączy się tylko z lokalnym serwerem modelu oraz ze sparowanymi prywatnymi adresami IPv4. Kod nie zawiera telemetrii. API modelu może zostać świadomie wystawione jako HTTP w prywatnym LAN; domyślnie słucha tylko na `127.0.0.1`.

## Granice zaufania

- GGUF i programy runtime są przetwarzane przez `llama.cpp`; używaj wag z zaufanego źródła.
- QR/kod parowania jest sekretem jednorazowej sesji. Nie publikuj go.
- Surowy protokół RPC jest eksperymentalny i dlatego nigdy nie słucha bezpośrednio w LAN.
- Aplikacja nie chroni przed złośliwym programem już działającym na tym samym koncie Windows lub Android.
- Wydanie testowe nie ma komercyjnego podpisu Authenticode. Suma i jawny kod pozwalają zweryfikować artefakt, ale nie zastępują reputacji podpisanego wydawcy.
- Bramka publikacji `.github/workflows/windows-defender.yml` odrzuca artefakt, jeśli aktualny Microsoft Defender na świeżym runnerze Windows wykryje program lub archiwum. To test w danym momencie, nie gwarancja zachowania wszystkich przyszłych wersji baz antywirusowych.

## Zgłaszanie

Nie publikuj sekretów sesji ani danych modelu. Problem bezpieczeństwa opisz w prywatnym zgłoszeniu GitHub Security Advisory repozytorium. Podaj wersję, system, kroki reprodukcji i — w przypadku alarmu antywirusa — pełną nazwę wykrycia oraz SHA-256. Nie wyłączaj antywirusa i nie dodawaj wyjątku na potrzeby testu.
