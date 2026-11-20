YourLM 0.2.0-rc1 — Windows x64 (wydanie do testów Lenovo + Redmi)
==========================

1. Pobierz YourLM.exe i uruchom go jako zwykły użytkownik.
2. Program sam przygotuje prywatny silnik w katalogu danych aplikacji i otworzy
   lokalny panel. Nie trzeba wypakowywać ZIP-a ani przechowywać plików runtime.
3. W panelu wybierz lokalny model .gguf i uruchom go najpierw bez workera.

Instalacja na telefonie przez USB
--------------------------------
Otwórz kartę „Telefon USB”. Włącz na telefonie Opcje programisty i Debugowanie
USB, podłącz przewód obsługujący dane, zaakceptuj odcisk RSA i wybierz
„Zainstaluj / uaktualnij YourLM”. Program korzysta z dołączonego ADB i zachowuje
dane aplikacji podczas uaktualnienia.

Aktualizacje
------------
YourLM sprawdza oficjalne wydania GitHub. Nowa wersja Windows może zostać
pobrana i podmieniona z panelu. Android pobiera APK i otwiera systemowy ekran
potwierdzenia instalacji. Modele i ustawienia pozostają w katalogach danych.

Wagi modeli nie są dołączone. Na pierwszy test wybierz model 3–4B Q4_K_M.
Domyślny adres 127.0.0.1 nie udostępnia API innym urządzeniom.

Aby dodać telefon jako worker, zatrzymaj model, wybierz na telefonie „Pomóż
laptopowi” i uruchom worker. W panelu Windows otwórz „Urządzenia → Dodaj
telefon” i przepisz adres oraz krótki kod. Oba urządzenia muszą być w tym samym
prywatnym Wi-Fi.

Jeżeli antywirus zgłosi plik, NIE wybieraj „Zezwalaj”, nie dodawaj wyjątku i nie
wyłączaj ochrony. Zachowaj nazwę wykrycia oraz nazwę pliku i zgłoś problem.
Sumy SHA-256 plików wydania znajdują się w SHA256SUMS.

Kod źródłowy i opis bezpieczeństwa:
https://github.com/rejson59/Your-LM
https://github.com/rejson59/Your-LM/blob/arena/395e51e3-your-lm/SECURITY.md
