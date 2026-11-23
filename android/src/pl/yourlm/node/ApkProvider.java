package pl.yourlm.node;

import android.content.*;
import android.database.Cursor;
import android.database.MatrixCursor;
import android.net.Uri;
import android.os.ParcelFileDescriptor;
import android.provider.OpenableColumns;
import java.io.File;
import java.io.FileNotFoundException;

/** Grants the system package installer read-only access to one downloaded APK. */
public final class ApkProvider extends ContentProvider {

  static final String FILE_NAME = "YourLM-update.apk";

  File updateFile() {
    return new File(new File(getContext().getCacheDir(), "updates"), FILE_NAME);
  }

  public boolean onCreate() {
    return true;
  }

  public String getType(Uri uri) {
    return "application/vnd.android.package-archive";
  }

  public ParcelFileDescriptor openFile(Uri uri, String mode)
    throws FileNotFoundException {
    if (!"r".equals(mode) || !FILE_NAME.equals(uri.getLastPathSegment())) {
      throw new FileNotFoundException("Nieznany plik aktualizacji");
    }
    File file = updateFile();
    if (!file.isFile()) throw new FileNotFoundException("Brak pobranego APK");
    return ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY);
  }

  public Cursor query(
    Uri uri,
    String[] projection,
    String selection,
    String[] selectionArgs,
    String sortOrder
  ) {
    File file = updateFile();
    MatrixCursor cursor = new MatrixCursor(
      new String[] { OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE }
    );
    cursor.addRow(new Object[] { FILE_NAME, file.length() });
    return cursor;
  }

  public Uri insert(Uri uri, ContentValues values) {
    throw new UnsupportedOperationException();
  }

  public int delete(Uri uri, String selection, String[] selectionArgs) {
    return 0;
  }

  public int update(
    Uri uri,
    ContentValues values,
    String selection,
    String[] selectionArgs
  ) {
    return 0;
  }
}
