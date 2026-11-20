# Bezpieczeństwo YourLM

## Co wykonuje wydanie Windows

`YourLM.exe` uruchamia lokalny panel HTTP wyłącznie na losowym porcie `127.0.0.1`, otwiera ten adres przez systemowe `ShellExecuteW`, a po wyborze modelu uruchamia prywatnie rozpakowany `llama-server.exe`. Opcjonalny tryb workera uruchamia `ggml-rpc-server.exe` tylko na loopbacku; do sieci prywatnej wystawiany jest oddzielny tunel TLS. QR przenosi odcisk certyfikatu i sekret sesji. Ręczne parowanie używa 100-bitowego kodu, który nie jest wysyłany: host weryfikuje HMAC losowego wyzwania, adresu i odcisku certyfikatu, po czym przypina certyfikat dla właściwego tunelu TLS.

Pojedynczy EXE zawiera skompresowany runtime, minimalny ADB i APK. Przy starcie rozpakowuje wersjonowaną kopię do `%AppData%\YourLM\components`, porównuje ją z osadzonym payloadem i odtwarza brakujące albo zmienione pliki. SHA-256 runtime jest dodatkowo sprawdzany bezpośrednio przed każdym uruchomieniem. ADB działa tylko na żądanie użytkownika do wykrycia telefonu oraz `adb install -r`.

Kontroler sprawdza publiczne wydania GitHub. Nowy `YourLM.exe` pobiera do prywatnego katalogu aktualizacji, ogranicza jego rozmiar i weryfikuje digest SHA-256 podany przez GitHub, po czym uruchamia nowy EXE jako jednorazowy helper podmiany. Nie używa PowerShella ani `rundll32`, nie instaluje usługi, nie zmienia rejestru, nie dodaje autostartu, nie prosi o administratora i nie dodaje wyjątków antywirusowych. Android pobiera wyłącznie asset `YourLM-Android-arm64.apk`, sprawdza digest, nazwę pakietu i nowszą wersję, a instalację zawsze przekazuje systemowemu ekranowi potwierdzenia.

Kontroler usuwa ze środowiska procesu potomnego zmienne `LLAMA_*` i `GGML_*`, które mogłyby włączyć inne zachowanie. Jawnie uruchamia `llama-server` w trybie offline, bez Web UI, MCP proxy i wbudowanych narzędzi plikowych/powłoki. Narzędzia agenta Continue działają po stronie Continue i nie wymagają serwerowych narzędzi `llama.cpp`.

Poza sprawdzaniem/pobraniem aktualizacji z `api.github.com` i adresu assetu GitHub kontroler łączy się tylko z lokalnym serwerem modelu oraz ze sparowanymi prywatnymi adresami IPv4. Kod nie zawiera telemetrii. API modelu może zostać świadomie wystawione jako HTTP w prywatnym LAN; domyślnie słucha tylko na `127.0.0.1`.

## Granice zaufania

- GGUF i programy runtime są przetwarzane przez `llama.cpp`; używaj wag z zaufanego źródła.
- QR i krótki kod parowania są sekretami jednorazowej sesji. Nie publikuj ich. Krótki kod ma cztery grupy po pięć znaków i 100 bitów losowości; nie jest to sześciocyfrowy PIN.
- Surowy protokół RPC jest eksperymentalny i dlatego nigdy nie słucha bezpośrednio w LAN.
- Aplikacja nie chroni przed złośliwym programem już działającym na tym samym koncie Windows lub Android.
- Wydanie testowe nie ma komercyjnego podpisu Authenticode. Suma i jawny kod pozwalają zweryfikować artefakt, ale nie zastępują reputacji podpisanego wydawcy.
- Bramka publikacji `.github/workflows/release.yml` skupia się na działaniu produktu: buduje przypięte silniki, sprawdza zgodność pakietów, uruchamia EXE oraz wykonuje przepływy workera, parowania i hosta Android.

## Zgłaszanie

Nie publikuj sekretów sesji ani danych modelu. Problem bezpieczeństwa opisz w prywatnym zgłoszeniu GitHub Security Advisory repozytorium. Podaj wersję, system, kroki reprodukcji i — w przypadku alarmu antywirusa — pełną nazwę wykrycia oraz SHA-256. Nie wyłączaj antywirusa i nie dodawaj wyjątku na potrzeby testu.
