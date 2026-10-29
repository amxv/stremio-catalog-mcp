package xyz.ashray.stremiotvremote;

import android.app.Application;
import com.google.firebase.FirebaseApp;
import com.google.firebase.FirebaseOptions;

public final class RemoteApplication extends Application {
    @Override public void onCreate() {
        super.onCreate();
        if (!BuildConfig.FIREBASE_APP_ID.isEmpty() &&
                !BuildConfig.FIREBASE_PROJECT_ID.isEmpty() &&
                !BuildConfig.FIREBASE_SENDER_ID.isEmpty() &&
                !BuildConfig.FIREBASE_API_KEY.isEmpty() &&
                FirebaseApp.getApps(this).isEmpty()) {
            FirebaseOptions options = new FirebaseOptions.Builder()
                    .setApplicationId(BuildConfig.FIREBASE_APP_ID)
                    .setProjectId(BuildConfig.FIREBASE_PROJECT_ID)
                    .setGcmSenderId(BuildConfig.FIREBASE_SENDER_ID)
                    .setApiKey(BuildConfig.FIREBASE_API_KEY)
                    .build();
            FirebaseApp.initializeApp(this, options);
        }
    }

    public static boolean firebaseConfigured() {
        return !BuildConfig.FIREBASE_APP_ID.isEmpty() &&
                !BuildConfig.FIREBASE_PROJECT_ID.isEmpty() &&
                !BuildConfig.FIREBASE_SENDER_ID.isEmpty() &&
                !BuildConfig.FIREBASE_API_KEY.isEmpty();
    }
}
