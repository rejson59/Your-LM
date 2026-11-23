YourLM 0.1.2 — Windows x64 (wydanie testowe)
================================================

1. Wypakuj CAŁY katalog YourLM-Windows-x64 w jedno miejsce.
2. Uruchom YourLM.exe jako zwykły użytkownik. Nie uruchamiaj jako administrator.
3. Program otworzy lokalny panel w domyślnej przeglądarce. Zostaw okno
   YourLM otwarte podczas pracy.
4. W panelu wybierz lokalny model .gguf i uruchom go najpierw bez workera.

Nie przenoś samego YourLM.exe: wymagane są dwa pliki w katalogu runtime.
Program nie pobiera i nie instaluje plików wykonywalnych. Przed każdym startem
sprawdza SHA-256 dołączonego llama-server albo ggml-rpc-server. Zmiana pliku
powoduje odmowę uruchomienia. Dokładne wersje źródeł i narzędzi są zapisane w
BUILDINFO.txt.

Wagi modeli nie są dołączone. Na pierwszy test wybierz model 3–4B Q4_K_M.
Domyślny adres 127.0.0.1 nie udostępnia API innym urządzeniom.

Aby dodać telefon, zatrzymaj model, wybierz na telefonie „Pomóż laptopowi” i
uruchom worker. W panelu Windows otwórz „Urządzenia → Dodaj telefon” i przepisz
adres oraz cztery grupy krótkiego kodu. Kamera laptopa i QR są tylko opcją;
telefon nie skanuje kodu. Oba urządzenia muszą być w tym samym prywatnym Wi-Fi.

Jeżeli antywirus zgłosi plik, NIE wybieraj „Zezwalaj”, nie dodawaj wyjątku i nie
wyłączaj ochrony. Zachowaj nazwę wykrycia oraz nazwę pliku i zgłoś problem w
repozytorium. Sumy SHA-256 pakietu znajdują się w SHA256SUMS.txt.

Kod źródłowy i opis bezpieczeństwa:
https://github.com/rejson59/Your-LM
https://github.com/rejson59/Your-LM/blob/arena/395e51e3-your-lm/SECURITY.md
