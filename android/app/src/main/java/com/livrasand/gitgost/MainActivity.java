package com.livrasand.gitgost;

import android.net.ConnectivityManager;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.NetworkRequest;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import androidx.annotation.NonNull;
import com.getcapacitor.BridgeActivity;

public class MainActivity extends BridgeActivity {

    private View offlineContainer;
    private ConnectivityManager connectivityManager;
    private ConnectivityManager.NetworkCallback networkCallback;
    private final Handler mainHandler = new Handler(Looper.getMainLooper());
    private boolean wasOffline = false;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        initOfflineView();
    }

    @Override
    public void onResume() {
        super.onResume();
        registerNetworkListener();
        updateNetworkStatus();
    }

    @Override
    public void onPause() {
        super.onPause();
        unregisterNetworkListener();
    }

    private void initOfflineView() {
        ViewGroup content = (ViewGroup) getWindow().getDecorView().findViewById(android.R.id.content);
        if (content == null) {
            return;
        }

        offlineContainer = getLayoutInflater().inflate(R.layout.offline_layout, content, false);
        offlineContainer.setVisibility(View.GONE);

        Button retryButton = offlineContainer.findViewById(R.id.offline_retry_button);
        if (retryButton != null) {
            retryButton.setOnClickListener(v -> onRetry());
        }

        content.addView(offlineContainer);
    }

    private void registerNetworkListener() {
        connectivityManager = (ConnectivityManager) getSystemService(ConnectivityManager.class);
        if (connectivityManager == null) {
            return;
        }

        NetworkRequest request = new NetworkRequest.Builder()
                .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                .build();

        networkCallback = new ConnectivityManager.NetworkCallback() {
            @Override
            public void onAvailable(@NonNull Network network) {
                mainHandler.post(MainActivity.this::updateNetworkStatus);
            }

            @Override
            public void onLost(@NonNull Network network) {
                mainHandler.post(MainActivity.this::updateNetworkStatus);
            }

            @Override
            public void onCapabilitiesChanged(@NonNull Network network, @NonNull NetworkCapabilities caps) {
                mainHandler.post(MainActivity.this::updateNetworkStatus);
            }
        };

        connectivityManager.registerNetworkCallback(request, networkCallback);
    }

    private void unregisterNetworkListener() {
        if (connectivityManager != null && networkCallback != null) {
            try {
                connectivityManager.unregisterNetworkCallback(networkCallback);
            } catch (IllegalArgumentException ignored) {
                // Already unregistered
            }
        }
        networkCallback = null;
    }

    private void updateNetworkStatus() {
        boolean online = isOnline();
        if (online && wasOffline) {
            hideOffline();
        } else if (!online && !wasOffline) {
            showOffline();
        }
    }

    private boolean isOnline() {
        if (connectivityManager == null) {
            return false;
        }

        Network activeNetwork = connectivityManager.getActiveNetwork();
        if (activeNetwork == null) {
            return false;
        }

        NetworkCapabilities caps = connectivityManager.getNetworkCapabilities(activeNetwork);
        return caps != null
                && caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                && caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED);
    }

    private void showOffline() {
        wasOffline = true;
        if (offlineContainer != null) {
            offlineContainer.setVisibility(View.VISIBLE);
        }
    }

    private void hideOffline() {
        wasOffline = false;
        if (offlineContainer != null) {
            offlineContainer.setVisibility(View.GONE);
        }
        reloadWebView();
    }

    private void onRetry() {
        reloadWebView();
        updateNetworkStatus();
    }

    private void reloadWebView() {
        if (getBridge() != null && getBridge().getWebView() != null) {
            getBridge().getWebView().reload();
        }
    }
}

