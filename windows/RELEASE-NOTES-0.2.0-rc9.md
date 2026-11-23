## YourLM 0.2.0-rc9 — migawki Redmi podczas ładowania

Fizyczna próba RC8 z modelem `huihui-ai-Huihui-gemma-4-26B-A4B-it-abliterated.GGUF` nadal zakończyła się po 168 sekundach komunikatem:

`ggml-rpc.cpp:699: Remote RPC server crashed or returned malformed response`

Windows zdążył wysłać 220 939 427 B i odebrać 3 962 B. Podane 1,25 MiB/s jest średnią **całego ruchu RPC hosta**, a nie czystym pomiarem przepustowości Wi‑Fi. Zapas 1024 MiB nie usunął awarii. Końcowe połączenie diagnostyczne RC8 zostało odrzucone, ponieważ listener Androida już nie działał, więc ten wynik nie rozstrzyga jeszcze, czy przyczyną było zakończenie procesu natywnego, wyjątek mostu Java, zatrzymanie usługi przez Androida czy inna ścieżka zamknięcia.

### Co zmienia RC9

- Windows pobiera uwierzytelnione `DIAG` z każdego workera od razu po starcie i następnie co 3 sekundy przez całą sesję modelu.
- Ostatnia poprawna migawka jest przechowywana po stronie Windows. Jeśli telefon po awarii odrzuca połączenie, ta migawka jest automatycznie dołączana do `engine.log` i do raportu z przycisku **Kopiuj diagnostykę**.
- Pomiar RAM i resident set workera w raporcie jest aktualizowany również w sesji, zamiast pozostawać początkowym pomiarem sprzed transferu.
- Android utrzymuje uwierzytelniony listener diagnostyczny do 10 minut po śmierci natywnego RPC i nie zamyka go już po pierwszym zapytaniu `DIAG`.
- Wyjątki obu kierunków mostu Java nie są już pomijane. Raport zawiera etap, czas i stos ostatniego wyjątku, a ograniczony dziennik zdarzeń oraz ostatnia migawka są zapisywane w prywatnych danych aplikacji.
- Raport zapisuje czas startu i zatrzymania RPC, kod wyjścia oraz rozpoznanie typowych sygnałów (`SIGABRT`, `SIGKILL`, `SIGSEGV`, `SIGBUS`, `SIGTERM`).
- Natywny worker zapisuje szczegółowy ślad poleceń RPC, dzięki czemu końcówka logu pokaże ostatnią komendę i tensor przed zerwaniem.
- Pozostają: automatyczny podział warstw, rezerwa 1024 MiB, strumieniowy cache, wskaźnik etapów i czasu, przycisk zatrzymania, tryb ciemny, instalacja per-user oraz Menu Start.
- RC9 wymaga zgodnego workera `b12562-ylm-rpc3`; starsze APK jest odrzucane przed startem modelu.

RC9 naprawia utratę dowodów diagnostycznych, ale nie udaje, że przyczyna samego zerwania RPC została już potwierdzona. Jedna kolejna próba fizyczna powinna zachować stan Redmi nawet wtedy, gdy cały listener zniknie.

### Aktualizacja i krótka próba

1. Uruchom nowe `YourLM.exe`.
2. W **Telefon USB** wybierz **Zainstaluj lub zaktualizuj**, aby wgrać APK RC9.
3. Uruchom worker na Redmi, usuń stary wpis telefonu w Windows i sparuj go ponownie.
4. Uruchom ten sam GGUF. Jeśli próba się nie powiedzie, wybierz w Windows **Kopiuj diagnostykę** i prześlij całość — nie trzeba osobno sprawdzać telefonu.
